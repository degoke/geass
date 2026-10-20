//go:build live

package aws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/compute"
)

// TestLiveLifecycle runs the driver against a real AWS account: it verifies the key, lists sizes, launches one
// t3.micro with Geass's cloud-init, waits for it to run and answer on port 22, finds it by its labels, opens and
// closes a rule on a security group the way the control plane firewall sync does, terminates it, and removes
// everything it made. It costs a few cents. Run it with
//
//	GEASS_LIVE_AWS_ACCESS_KEY_ID=... GEASS_LIVE_AWS_SECRET_ACCESS_KEY=... go test -tags live -run TestLive -v ./pkg/compute/aws
//
// GEASS_LIVE_AWS_REGION defaults to eu-north-1. Whatever happens, the cleanup terminates every instance and deletes
// every security group that carries this run's cluster label.
func TestLiveLifecycle(t *testing.T) {
	accessKey, secretKey := os.Getenv("GEASS_LIVE_AWS_ACCESS_KEY_ID"), os.Getenv("GEASS_LIVE_AWS_SECRET_ACCESS_KEY")
	if accessKey == "" || secretKey == "" {
		t.Skip("set GEASS_LIVE_AWS_ACCESS_KEY_ID and GEASS_LIVE_AWS_SECRET_ACCESS_KEY to run against AWS")
	}
	region := os.Getenv("GEASS_LIVE_AWS_REGION")
	if region == "" {
		region = "eu-north-1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	run := "live-" + hex.EncodeToString(suffix)
	owner := compute.Owner{Cluster: run, Pool: "probe", Regions: []string{region}}
	p := New(accessKey, secretKey, region, compute.Options{})

	t.Cleanup(func() { sweepRun(t, p, region, owner, run) })

	account, err := p.Verify(ctx)
	require.NoError(t, err)
	t.Logf("verified: %s, warnings %v", account.Name, account.Warnings)
	require.Regexp(t, `^AWS account \d{12}$`, account.Name, "the account is named from STS's answer")
	require.Empty(t, account.Warnings, "the key is allowed to launch tagged instances")

	sizes, err := p.Sizes(ctx, region)
	require.NoError(t, err)
	require.NotEmpty(t, sizes)
	ids := make([]string, 0, len(sizes))
	for _, s := range sizes {
		ids = append(ids, s.ID)
	}
	require.Contains(t, ids, "t3.micro", "the smallest x86 size is offered")
	t.Logf("%d sizes, the first %v", len(sizes), sizes[:3])

	hostKey, err := compute.GenerateKeyPair("live-test")
	require.NoError(t, err)
	login, err := compute.GenerateKeyPair("live-login")
	require.NoError(t, err)
	userData := compute.CloudInit{AuthorizedKeys: []string{login.Public}, HostKey: &hostKey, Hostname: run + "-probe"}.Render()
	spec := compute.InstanceSpec{
		Name: run + "-probe", Region: region, Size: "t3.micro", UserData: userData, AuthorizedKeys: []string{login.Public},
		Labels: owner.Labels(run + "-probe"),
	}

	created, err := p.Create(ctx, spec)
	require.NoError(t, err)
	t.Logf("created %s (%s)", created.ID, created.State)
	require.True(t, strings.HasPrefix(created.ID, region+"/i-"), "the id says where the machine is: %s", created.ID)

	again, err := p.Create(ctx, spec)
	require.NoError(t, err)
	require.Equal(t, created.ID, again.ID, "a repeated create is the same machine, not a second one")

	var running compute.Instance
	require.Eventually(t, func() bool {
		running, err = p.Get(ctx, created.ID)
		return err == nil && running.Ready()
	}, 5*time.Minute, 5*time.Second, "the machine runs and has a public address")
	t.Logf("running at %s (private %s), size %s", running.PublicIPv4, running.PrivateIPv4, running.Size)
	require.Equal(t, "t3.micro", running.Size)
	require.Equal(t, "true", running.Labels[compute.LabelManaged])
	require.Equal(t, run, running.Labels[compute.LabelCluster])

	var banner string
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(running.PublicIPv4, "22"), 5*time.Second)
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 64)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _ := conn.Read(buf)
		banner = string(buf[:n])
		return strings.HasPrefix(banner, "SSH-")
	}, 5*time.Minute, 5*time.Second, "sshd answers through the security group Geass made")
	t.Logf("port 22 answers: %q", strings.TrimSpace(banner))

	listed, err := p.List(ctx, owner)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)
	other, err := p.List(ctx, compute.Owner{Cluster: run + "-other", Pool: "probe", Regions: []string{region}})
	require.NoError(t, err)
	require.Empty(t, other, "another owner's list never contains this machine")

	// The firewall sync: a stand-in for the control plane's group, with one rule of ours opened and closed.
	group := makeGroup(ctx, t, p, region, run)
	rule := compute.AccessRule{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.7/32"}
	target := compute.AccessTarget{ID: group, Region: region}
	require.NoError(t, p.AllowAccess(ctx, target, []compute.AccessRule{rule}))
	require.NoError(t, p.AllowAccess(ctx, target, []compute.AccessRule{rule}), "opening what is open is not an error")
	require.Contains(t, groupRules(ctx, t, p, region, group), "tcp/6443/203.0.113.7/32")
	require.NoError(t, p.RevokeAccess(ctx, target, []compute.AccessRule{rule}))
	require.NoError(t, p.RevokeAccess(ctx, target, []compute.AccessRule{rule}), "closing what is closed is not an error")
	require.NotContains(t, groupRules(ctx, t, p, region, group), "tcp/6443/203.0.113.7/32")

	require.NoError(t, p.Delete(ctx, created.ID))
	require.NoError(t, p.Delete(ctx, created.ID), "deleting twice is not an error")
	require.Eventually(t, func() bool {
		gone, err := p.Get(ctx, created.ID)
		return err != nil || !gone.Alive()
	}, 5*time.Minute, 5*time.Second, "the machine is terminated")
	left, err := p.List(ctx, owner)
	require.NoError(t, err)
	require.Empty(t, alive(left), "nothing alive is left to bill (EC2 lists a terminated instance for an hour)")
}

// alive leaves out the machines that are already gone or going: a terminated instance stays in EC2's lists for about
// an hour and costs nothing.
func alive(machines []compute.Instance) []compute.Instance {
	var out []compute.Instance
	for _, m := range machines {
		if m.Alive() {
			out = append(out, m)
		}
	}
	return out
}

func makeGroup(ctx context.Context, t *testing.T, p *Provider, region, run string) string {
	t.Helper()
	values := url.Values{paramAction: {"CreateSecurityGroup"}, "GroupName": {"geass-" + run + "-cp"}, "GroupDescription": {"live test stand-in for a control plane"}}
	values.Set("TagSpecification.1.ResourceType", "security-group")
	tagParams(values, "TagSpecification.1", map[string]string{compute.LabelManaged: "true", compute.LabelCluster: run})
	body, err := p.ec2(ctx, region, values)
	require.NoError(t, err)
	var made struct {
		ID string `xml:"groupId"`
	}
	require.NoError(t, xml.Unmarshal(body, &made))
	require.NotEmpty(t, made.ID)
	return made.ID
}

func groupRules(ctx context.Context, t *testing.T, p *Provider, region, group string) []string {
	t.Helper()
	body, err := p.ec2(ctx, region, url.Values{paramAction: {"DescribeSecurityGroups"}, "GroupId.1": {group}})
	require.NoError(t, err)
	var parsed struct {
		Rules []struct {
			Protocol string `xml:"ipProtocol"`
			From     string `xml:"fromPort"`
			Ranges   []struct {
				CIDR        string `xml:"cidrIp"`
				Description string `xml:"description"`
			} `xml:"ipRanges>item"`
		} `xml:"securityGroupInfo>item>ipPermissions>item"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed))
	var rules []string
	for _, r := range parsed.Rules {
		for _, c := range r.Ranges {
			rules = append(rules, r.Protocol+"/"+r.From+"/"+c.CIDR)
			if r.From == "6443" {
				require.Equal(t, accessDescription, c.Description, "the rule is marked as Geass's")
			}
		}
	}
	return rules
}

// sweepRun terminates every instance and deletes every security group of this run, so that a failed test leaves
// nothing that bills or clutters the account.
func sweepRun(t *testing.T, p *Provider, region string, owner compute.Owner, run string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if machines, err := p.List(ctx, owner); err == nil {
		for _, m := range alive(machines) {
			t.Logf("sweeping instance %s", m.ID)
			_ = p.Delete(ctx, m.ID)
		}
	}
	require.Eventually(t, func() bool {
		machines, err := p.List(ctx, owner)
		return err == nil && len(alive(machines)) == 0
	}, 5*time.Minute, 5*time.Second, "no instance of the run is left")
	// A terminated instance is listed as terminating for a while; a group cannot be deleted until it is gone.
	body, err := p.ec2(ctx, region, url.Values{paramAction: {"DescribeSecurityGroups"},
		"Filter.1.Name": {"tag:" + compute.LabelCluster}, "Filter.1.Value.1": {run}})
	if err != nil {
		t.Logf("could not list the run's security groups: %v", err)
		return
	}
	var groups struct {
		IDs []string `xml:"securityGroupInfo>item>groupId"`
	}
	_ = xml.Unmarshal(body, &groups)
	for _, id := range groups.IDs {
		deleted := false
		for range 3 {
			if _, err := p.ec2(ctx, region, url.Values{paramAction: {"DeleteSecurityGroup"}, "GroupId": {id}}); err == nil {
				deleted = true
				break
			}
			time.Sleep(10 * time.Second)
		}
		// The key the policy describes may not delete groups (Geass never does), so this is best effort: whoever runs
		// the test sweeps what is left with an administrator's key, by the run's cluster tag.
		t.Logf("security group %s of run %s deleted: %v", id, run, deleted)
	}
}
