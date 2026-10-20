// Package fake is an in-memory compute provider for tests of everything that creates machines: the node controller,
// the pool controller and the autoscaler. It behaves like a cloud does where that matters (machines start pending,
// a repeated create of the same name returns the first machine, a deleted machine stays visible for a moment) and
// can be told to fail in the ways clouds fail.
package fake

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// Provider is a compute.Provider that keeps its machines in memory.
type Provider struct {
	mu sync.Mutex

	machines map[string]*machine
	seq      int

	// PendingPolls is how many times Get reports a new machine as pending before it is running.
	PendingPolls int
	// CreateError, when set, is returned by every Create.
	CreateError error
	// CreateErrors are returned by the next Creates, one each, before CreateError applies.
	CreateErrors []error
	// DeleteError, when set, is returned by every Delete.
	DeleteError error
	// VerifyAccount and VerifyError are what Verify returns.
	VerifyAccount compute.Account
	VerifyError   error
	// SizeList is what Sizes returns.
	SizeList []compute.Size
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// NeverReady keeps machines pending for ever, the way a machine that never boots does.
	NeverReady bool

	// AccessError, when set, is returned by AllowAccess and RevokeAccess.
	AccessError error
	// Access holds the rules that are open now, as "protocol/port/cidr" in the target's firewall.
	Access map[string]bool

	// Created and Deleted record every call, in order.
	Created []compute.InstanceSpec
	Deleted []string
	// Calls counts calls by method name.
	Calls map[string]int
}

type machine struct {
	instance compute.Instance
	polls    int
}

// New returns an empty provider.
func New() *Provider {
	return &Provider{machines: map[string]*machine{}, Calls: map[string]int{}}
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) count(method string) {
	if p.Calls == nil {
		p.Calls = map[string]int{}
	}
	p.Calls[method]++
}

// Verify implements compute.Provider.
func (p *Provider) Verify(context.Context) (compute.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("Verify")
	if p.VerifyError != nil {
		return compute.Account{}, p.VerifyError
	}
	account := p.VerifyAccount
	if account.Name == "" {
		account.Name = "fake"
	}
	return account, nil
}

// Sizes implements compute.Provider.
func (p *Provider) Sizes(context.Context, string) ([]compute.Size, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("Sizes")
	if p.SizeList != nil {
		return append([]compute.Size(nil), p.SizeList...), nil
	}
	return []compute.Size{{ID: "small", VCPUs: 2, MemoryMiB: 4096, DiskGB: 40, HourlyPrice: 0.01, Currency: "USD", Arch: "amd64"}}, nil
}

// Create implements compute.Provider.
func (p *Provider) Create(_ context.Context, spec compute.InstanceSpec) (compute.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("Create")
	if len(p.CreateErrors) > 0 {
		err := p.CreateErrors[0]
		p.CreateErrors = p.CreateErrors[1:]
		return compute.Instance{}, err
	}
	if p.CreateError != nil {
		return compute.Instance{}, p.CreateError
	}
	for _, m := range p.machines {
		// Names are unique within an owner, which is how a repeated create finds its first machine.
		if m.instance.Name == spec.Name && m.instance.Alive() && m.instance.Labels[compute.LabelCluster] == spec.Labels[compute.LabelCluster] {
			return m.instance, nil
		}
	}
	p.Created = append(p.Created, spec)
	p.seq++
	id := fmt.Sprintf("i-%04d", p.seq)
	labels := maps.Clone(spec.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	m := &machine{instance: compute.Instance{
		ID: id, Name: spec.Name, Region: spec.Region, Size: spec.Size,
		State: compute.StatePending, Labels: labels, CreatedAt: p.now(),
	}}
	p.machines[id] = m
	return m.instance, nil
}

// Get implements compute.Provider.
func (p *Provider) Get(_ context.Context, id string) (compute.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("Get")
	m, ok := p.machines[id]
	if !ok || m.instance.State == compute.StateGone {
		return compute.Instance{}, compute.ErrNotFound
	}
	p.advance(m)
	return m.instance, nil
}

// advance moves a pending machine on by one poll.
func (p *Provider) advance(m *machine) {
	if m.instance.State != compute.StatePending || p.NeverReady {
		return
	}
	m.polls++
	if m.polls > p.PendingPolls {
		m.instance.State = compute.StateRunning
		m.instance.PublicIPv4 = fmt.Sprintf("203.0.113.%d", 10+p.seqOf(m.instance.ID))
		m.instance.PrivateIPv4 = fmt.Sprintf("10.0.0.%d", 10+p.seqOf(m.instance.ID))
	}
}

func (p *Provider) seqOf(id string) int {
	var n int
	_, _ = fmt.Sscanf(id, "i-%d", &n)
	return n
}

// List implements compute.Provider.
func (p *Provider) List(_ context.Context, owner compute.Owner) ([]compute.Instance, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("List")
	var out []compute.Instance
	for _, m := range p.machines {
		if m.instance.State == compute.StateGone || !owner.Owns(m.instance.Labels) {
			continue
		}
		p.advance(m)
		out = append(out, m.instance)
	}
	slices.SortFunc(out, func(a, b compute.Instance) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Delete implements compute.Provider.
func (p *Provider) Delete(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("Delete")
	if p.DeleteError != nil {
		return p.DeleteError
	}
	m, ok := p.machines[id]
	if !ok {
		return nil
	}
	p.Deleted = append(p.Deleted, id)
	m.instance.State = compute.StateGone
	return nil
}

// Instances returns every machine that has not been deleted.
func (p *Provider) Instances() []compute.Instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []compute.Instance
	for _, m := range p.machines {
		if m.instance.State != compute.StateGone {
			out = append(out, m.instance)
		}
	}
	slices.SortFunc(out, func(a, b compute.Instance) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Adopt puts a machine that already exists into the provider, as one created by an earlier run: it is running at
// once and carries the labels.
func (p *Provider) Adopt(name string, labels map[string]string, age time.Duration) compute.Instance {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	id := fmt.Sprintf("i-%04d", p.seq)
	cp := maps.Clone(labels)
	if cp == nil {
		cp = map[string]string{}
	}
	m := &machine{instance: compute.Instance{
		ID: id, Name: name, State: compute.StateRunning, Labels: cp,
		PublicIPv4: fmt.Sprintf("203.0.113.%d", 10+p.seq), CreatedAt: p.now().Add(-age),
	}}
	p.machines[id] = m
	return m.instance
}

// Stop turns a machine off without removing it, the way a machine whose creation stopped half way is found.
func (p *Provider) Stop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.machines[id]; ok {
		m.instance.State = compute.StateStopped
	}
}

// Kill removes a machine without a Delete call, the way a cloud takes a spot instance or an operator deletes one
// in the console.
func (p *Provider) Kill(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.machines[id]; ok {
		m.instance.State = compute.StateGone
	}
}

// AllowAccess implements compute.AccessManager.
func (p *Provider) AllowAccess(_ context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("AllowAccess")
	if p.AccessError != nil {
		return p.AccessError
	}
	if p.Access == nil {
		p.Access = map[string]bool{}
	}
	for _, r := range rules {
		p.Access[fmt.Sprintf("%s:%s/%d/%s", target.ID, r.Protocol, r.Port, r.CIDR)] = true
	}
	return nil
}

// RevokeAccess implements compute.AccessManager.
func (p *Provider) RevokeAccess(_ context.Context, target compute.AccessTarget, rules []compute.AccessRule) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.count("RevokeAccess")
	if p.AccessError != nil {
		return p.AccessError
	}
	for _, r := range rules {
		delete(p.Access, fmt.Sprintf("%s:%s/%d/%s", target.ID, r.Protocol, r.Port, r.CIDR))
	}
	return nil
}

var (
	_ compute.Provider      = (*Provider)(nil)
	_ compute.AccessManager = (*Provider)(nil)
)
