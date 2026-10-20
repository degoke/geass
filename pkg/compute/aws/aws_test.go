package aws

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/degoke/geass/pkg/compute"
)

type fakeInstance struct {
	id, name, state, itype, az string
	tags                       map[string]string
}

// fakeEC2 is the part of the EC2 Query API Geass uses, in memory. Every request is checked for a SigV4 signature.
type fakeEC2 struct {
	t         *testing.T
	instances []*fakeInstance
	groups    map[string]map[string]any // id -> {name, rules []permission}
	launches  []url.Values
	calls     []string
	seq       int
	// fail makes the next call of an action fail with a code.
	fail         map[string]string
	pageSize     int
	descriptions []string
	arch         string
	dryRunResult string
}

func newFakeEC2(t *testing.T) (*fakeEC2, *Provider) {
	f := &fakeEC2{t: t, groups: map[string]map[string]any{}, fail: map[string]string{}, arch: "x86_64", dryRunResult: "DryRunOperation"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, New("AKIDEXAMPLE", "secret", "eu-north-1", compute.Options{BaseURL: srv.URL})
}

func (f *fakeEC2) xmlError(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<Response><Errors><Error><Code>%s</Code><Message>%s</Message></Error></Errors><RequestID>r</RequestID></Response>`, code, message)
}

func (f *fakeEC2) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") {
		f.t.Errorf("request is not signed: %q", r.Header.Get("Authorization"))
	}
	_ = r.ParseForm()
	form := r.PostForm
	action := form.Get("Action")
	f.calls = append(f.calls, action)
	if code, ok := f.fail[action]; ok {
		delete(f.fail, action)
		status := 400
		if code == "RequestLimitExceeded" {
			status = 503
		}
		f.xmlError(w, status, code, "refused")
		return
	}
	switch action {
	case "GetCallerIdentity":
		// Real STS answers in JSON when the request says JSON is wanted, and in XML otherwise.
		if strings.Contains(r.Header.Get("Accept"), "json") {
			_, _ = fmt.Fprint(w, `{"GetCallerIdentityResponse":{"GetCallerIdentityResult":{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:user/geass"}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Account>123456789012</Account><Arn>arn:aws:iam::123456789012:user/geass</Arn></GetCallerIdentityResult></GetCallerIdentityResponse>`)
	case "DescribeInstanceTypes":
		if t := form.Get("InstanceType.1"); t != "" {
			_, _ = fmt.Fprintf(w, `<R><instanceTypeSet><item><instanceType>%s</instanceType><processorInfo><supportedArchitectures><item>%s</item></supportedArchitectures></processorInfo></item></instanceTypeSet></R>`, t, f.arch)
			return
		}
		_, _ = fmt.Fprint(w, `<R><instanceTypeSet>
<item><instanceType>t3.large</instanceType><vCpuInfo><defaultVCpus>2</defaultVCpus></vCpuInfo><memoryInfo><sizeInMiB>8192</sizeInMiB></memoryInfo><processorInfo><supportedArchitectures><item>x86_64</item></supportedArchitectures></processorInfo></item>
<item><instanceType>t3.medium</instanceType><vCpuInfo><defaultVCpus>2</defaultVCpus></vCpuInfo><memoryInfo><sizeInMiB>4096</sizeInMiB></memoryInfo><processorInfo><supportedArchitectures><item>x86_64</item></supportedArchitectures></processorInfo></item>
<item><instanceType>m7g.huge</instanceType><vCpuInfo><defaultVCpus>64</defaultVCpus></vCpuInfo><memoryInfo><sizeInMiB>1</sizeInMiB></memoryInfo></item>
</instanceTypeSet></R>`)
	case "DescribeImages":
		_, _ = fmt.Fprintf(w, `<R><imagesSet><item><imageId>ami-old</imageId><creationDate>2026-01-01T00:00:00Z</creationDate></item><item><imageId>ami-new-%s</imageId><creationDate>2026-09-01T00:00:00Z</creationDate></item></imagesSet></R>`, fmt.Sprint(strings.Contains(form.Get("Filter.1.Value.1"), "arm64")))
	case "DescribeSubnets":
		_, _ = fmt.Fprint(w, `<R><subnetSet><item><vpcId>vpc-1</vpcId></item></subnetSet></R>`)
	case "DescribeSecurityGroups":
		var items strings.Builder
		for id, g := range f.groups {
			if g["name"] != form.Get("Filter.1.Value.1") {
				continue
			}
			_, _ = fmt.Fprintf(&items, "<item><groupId>%s</groupId><ipPermissions>", id)
			for _, p := range g["rules"].([]permission) {
				_, _ = fmt.Fprintf(&items, "<item><ipProtocol>%s</ipProtocol><fromPort>%d</fromPort><toPort>%d</toPort><ipRanges><item><cidrIp>%s</cidrIp></item></ipRanges></item>", p.Protocol, p.Port, p.Port, p.CIDR)
			}
			items.WriteString("</ipPermissions></item>")
		}
		_, _ = fmt.Fprintf(w, `<R><securityGroupInfo>%s</securityGroupInfo></R>`, items.String())
	case "CreateSecurityGroup":
		f.seq++
		id := fmt.Sprintf("sg-%d", f.seq)
		f.groups[id] = map[string]any{"name": form.Get("GroupName"), "rules": []permission{}, "vpc": form.Get("VpcId")}
		_, _ = fmt.Fprintf(w, `<R><groupId>%s</groupId></R>`, id)
	case "AuthorizeSecurityGroupIngress", "RevokeSecurityGroupIngress":
		g := f.groups[form.Get("GroupId")]
		rules := g["rules"].([]permission)
		// EC2 refuses the whole request when one of its rules is there already (or, for a revoke, is not).
		for i := 1; form.Get(fmt.Sprintf("IpPermissions.%d.IpProtocol", i)) != ""; i++ {
			var port int
			_, _ = fmt.Sscan(form.Get(fmt.Sprintf("IpPermissions.%d.FromPort", i)), &port)
			rule := permission{form.Get(fmt.Sprintf("IpPermissions.%d.IpProtocol", i)), port, form.Get(fmt.Sprintf("IpPermissions.%d.IpRanges.1.CidrIp", i))}
			present := false
			for _, r := range rules {
				present = present || r == rule
			}
			if action == "AuthorizeSecurityGroupIngress" && present {
				f.xmlError(w, 400, "InvalidPermission.Duplicate", "the rule exists")
				return
			}
			if action == "RevokeSecurityGroupIngress" && !present {
				f.xmlError(w, 400, "InvalidPermission.NotFound", "no such rule")
				return
			}
			if action == "AuthorizeSecurityGroupIngress" {
				f.descriptions = append(f.descriptions, form.Get(fmt.Sprintf("IpPermissions.%d.IpRanges.1.Description", i)))
			}
		}
		for i := 1; form.Get(fmt.Sprintf("IpPermissions.%d.IpProtocol", i)) != ""; i++ {
			var port int
			_, _ = fmt.Sscan(form.Get(fmt.Sprintf("IpPermissions.%d.FromPort", i)), &port)
			rule := permission{form.Get(fmt.Sprintf("IpPermissions.%d.IpProtocol", i)), port, form.Get(fmt.Sprintf("IpPermissions.%d.IpRanges.1.CidrIp", i))}
			if action == "AuthorizeSecurityGroupIngress" {
				rules = append(rules, rule)
			} else {
				var kept []permission
				for _, r := range rules {
					if r != rule {
						kept = append(kept, r)
					}
				}
				rules = kept
			}
		}
		g["rules"] = rules
		_, _ = fmt.Fprint(w, `<R><return>true</return></R>`)
	case "RunInstances":
		if form.Get("DryRun") == "true" {
			f.xmlError(w, 412, f.dryRunResult, "Request would have succeeded")
			return
		}
		f.launches = append(f.launches, form)
		for _, i := range f.instances {
			if i.tags["Name"] == tagValue(form, "Name") && i.state != "terminated" {
				_, _ = fmt.Fprint(w, instancesXML("R", "instancesSet", i))
				return
			}
		}
		f.seq++
		inst := &fakeInstance{id: fmt.Sprintf("i-%04d", f.seq), name: tagValue(form, "Name"), state: "pending", itype: form.Get("InstanceType"), az: "eu-north-1a", tags: allTags(form)}
		f.instances = append(f.instances, inst)
		_, _ = fmt.Fprint(w, instancesXML("RunInstancesResponse", "instancesSet", inst))
	case "DescribeInstances":
		var matched []*fakeInstance
		for _, i := range f.instances {
			if id := form.Get("InstanceId.1"); id != "" && id != i.id {
				continue
			}
			if !matchesFilters(form, i) {
				continue
			}
			matched = append(matched, i)
		}
		if len(matched) == 0 && form.Get("InstanceId.1") != "" {
			f.xmlError(w, 400, "InvalidInstanceID.NotFound", "The instance ID does not exist")
			return
		}
		start := 0
		if tok := form.Get("NextToken"); tok != "" {
			_, _ = fmt.Sscan(tok, &start)
		}
		next := ""
		if f.pageSize > 0 && start+f.pageSize < len(matched) {
			next = fmt.Sprint(start + f.pageSize)
			matched = matched[start : start+f.pageSize]
		} else {
			matched = matched[start:]
		}
		var b strings.Builder
		b.WriteString("<DescribeInstancesResponse><reservationSet>")
		for _, i := range matched {
			b.WriteString("<item>" + instanceSetXML(i) + "</item>")
		}
		_, _ = fmt.Fprintf(&b, "</reservationSet><nextToken>%s</nextToken></DescribeInstancesResponse>", next)
		_, _ = fmt.Fprint(w, b.String())
	case "TerminateInstances":
		for _, i := range f.instances {
			if i.id == form.Get("InstanceId.1") {
				i.state = "terminated"
				_, _ = fmt.Fprint(w, `<R/>`)
				return
			}
		}
		f.xmlError(w, 400, "InvalidInstanceID.NotFound", "gone")
	default:
		f.xmlError(w, 400, "InvalidAction", "unknown "+action)
	}
}

func tagValue(form url.Values, key string) string {
	for i := 1; form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", i)) != ""; i++ {
		if form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", i)) == key {
			return form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Value", i))
		}
	}
	return ""
}

func allTags(form url.Values) map[string]string {
	tags := map[string]string{}
	for i := 1; form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", i)) != ""; i++ {
		tags[form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Key", i))] = form.Get(fmt.Sprintf("TagSpecification.1.Tag.%d.Value", i))
	}
	return tags
}

func matchesFilters(form url.Values, i *fakeInstance) bool {
	for n := 1; form.Get(fmt.Sprintf("Filter.%d.Name", n)) != ""; n++ {
		name := form.Get(fmt.Sprintf("Filter.%d.Name", n))
		var values []string
		for v := 1; form.Get(fmt.Sprintf("Filter.%d.Value.%d", n, v)) != ""; v++ {
			values = append(values, form.Get(fmt.Sprintf("Filter.%d.Value.%d", n, v)))
		}
		var have string
		switch {
		case name == "instance-state-name":
			have = i.state
		case strings.HasPrefix(name, "tag:"):
			have = i.tags[strings.TrimPrefix(name, "tag:")]
		}
		ok := false
		for _, v := range values {
			if v == have {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func instanceSetXML(i *fakeInstance) string {
	var tags strings.Builder
	keys := make([]string, 0, len(i.tags))
	for k := range i.tags {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		_, _ = fmt.Fprintf(&tags, "<item><key>%s</key><value>%s</value></item>", k, i.tags[k])
	}
	ip := ""
	if i.state == "running" {
		ip = "<ipAddress>203.0.113.9</ipAddress><privateIpAddress>10.0.0.9</privateIpAddress>"
	}
	return fmt.Sprintf(`<instancesSet><item><instanceId>%s</instanceId><instanceState><name>%s</name></instanceState>%s<instanceType>%s</instanceType><launchTime>2026-10-19T10:00:00.000Z</launchTime><placement><availabilityZone>%s</availabilityZone></placement><tagSet>%s</tagSet></item></instancesSet>`,
		i.id, i.state, ip, i.itype, i.az, tags.String())
}

func instancesXML(root, _ string, i *fakeInstance) string {
	return "<" + root + ">" + instanceSetXML(i) + "</" + root + ">"
}

func (f *fakeEC2) run(id string) {
	for _, i := range f.instances {
		if i.id == id {
			i.state = "running"
		}
	}
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: "eu-north-1", Size: "t3.medium", UserData: "#cloud-config\nusers: []\n", Labels: owner.Labels(name),
		ControlPlaneCIDRs: []string{"203.0.113.5/32"}, MultiSite: true,
	}
}

func TestCreateLaunchesATaggedIMDSv2MachineFromTheNewestUbuntuImage(t *testing.T) {
	f, p := newFakeEC2(t)

	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID != "eu-north-1/i-0002" || instance.Name != "burst-a" || instance.State != compute.StatePending {
		t.Errorf("instance = %+v", instance)
	}
	if len(f.launches) != 1 {
		t.Fatalf("%d launches", len(f.launches))
	}
	launch := f.launches[0]
	if launch.Get("ImageId") != "ami-new-false" || launch.Get("InstanceType") != "t3.medium" {
		t.Errorf("image %q type %q", launch.Get("ImageId"), launch.Get("InstanceType"))
	}
	if launch.Get("MetadataOptions.HttpTokens") != "required" || launch.Get("MetadataOptions.HttpPutResponseHopLimit") != "1" {
		t.Error("the instance metadata service must need IMDSv2 and stop at one hop, so pods cannot read the instance's credentials")
	}
	if launch.Get("CreditSpecification.CpuCredits") != "standard" {
		t.Error("a burstable instance must not run on unlimited credits")
	}
	raw, _ := base64.StdEncoding.DecodeString(launch.Get("UserData"))
	if string(raw) != "#cloud-config\nusers: []\n" {
		t.Errorf("user data = %q", raw)
	}
	for _, resource := range []string{"1", "2"} {
		if tags := tagsOf(launch, "TagSpecification."+resource); tags[compute.LabelManaged] != "true" || tags["Name"] != "burst-a" || tags[compute.LabelNode] != "burst-a" {
			t.Errorf("tags of resource %s = %v", resource, tags)
		}
	}
	if launch.Get("TagSpecification.1.ResourceType") != "instance" || launch.Get("TagSpecification.2.ResourceType") != "volume" {
		t.Error("both the instance and its volume must be tagged when they are created")
	}
	if launch.Get("BlockDeviceMapping.1.Ebs.DeleteOnTermination") != "true" || launch.Get("BlockDeviceMapping.1.Ebs.VolumeSize") != "30" {
		t.Error("the root volume must go with the instance")
	}
	if launch.Get("ClientToken") == "" || launch.Get("SecurityGroupId.1") == "" {
		t.Error("a launch needs an idempotency token and the cluster's security group")
	}
}

func tagsOf(form url.Values, prefix string) map[string]string {
	tags := map[string]string{}
	for i := 1; form.Get(fmt.Sprintf("%s.Tag.%d.Key", prefix, i)) != ""; i++ {
		tags[form.Get(fmt.Sprintf("%s.Tag.%d.Key", prefix, i))] = form.Get(fmt.Sprintf("%s.Tag.%d.Value", prefix, i))
	}
	return tags
}

func TestAnArmSizeGetsAnArmImage(t *testing.T) {
	f, p := newFakeEC2(t)
	f.arch = "arm64"
	s := spec("burst-a")
	s.Size = "t4g.medium"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if got := f.launches[0].Get("ImageId"); got != "ami-new-true" {
		t.Errorf("image = %q, want the arm64 one", got)
	}
}

func TestTheSecurityGroupAllowsInOnlyWhatTheClusterNeeds(t *testing.T) {
	f, p := newFakeEC2(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups", len(f.groups))
	}
	for _, g := range f.groups {
		rules := g["rules"].([]permission)
		got := map[string]bool{}
		for _, r := range rules {
			got[fmt.Sprintf("%s/%d/%s", r.Protocol, r.Port, r.CIDR)] = true
		}
		want := []string{"tcp/22/203.0.113.5/32", "tcp/10250/203.0.113.5/32", "udp/51820/0.0.0.0/0"}
		if len(got) != 3 {
			t.Errorf("rules = %v", got)
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("missing rule %s in %v", w, got)
			}
		}
	}
}

func TestTheGroupIsReusedAndItsManagedRulesFollowTheControlPlane(t *testing.T) {
	f, p := newFakeEC2(t)
	if _, err := p.Create(context.Background(), spec("burst-a")); err != nil {
		t.Fatal(err)
	}
	for _, g := range f.groups { // a rule of the user's, on a port Geass does not manage
		g["rules"] = append(g["rules"].([]permission), permission{"tcp", 8080, "10.0.0.0/8"})
	}
	moved := spec("burst-b")
	moved.ControlPlaneCIDRs = []string{"203.0.113.77/32"}
	if _, err := p.Create(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 1 {
		t.Fatalf("%d groups, want the one to be reused", len(f.groups))
	}
	for _, g := range f.groups {
		text := make([]string, 0, len(g["rules"].([]permission)))
		for _, r := range g["rules"].([]permission) {
			text = append(text, fmt.Sprintf("%s/%d/%s", r.Protocol, r.Port, r.CIDR))
		}
		joined := strings.Join(text, " ")
		if strings.Contains(joined, "203.0.113.5/32") || !strings.Contains(joined, "tcp/22/203.0.113.77/32") {
			t.Errorf("rules = %s", joined)
		}
		if !strings.Contains(joined, "tcp/8080/10.0.0.0/8") {
			t.Errorf("the user's own rule was removed: %s", joined)
		}
	}
}

func TestASecurityGroupTheUserNamesIsUsedAsItIs(t *testing.T) {
	f, p := newFakeEC2(t)
	s := spec("burst-a")
	s.Settings = map[string]string{SettingSecurityGroupID: "sg-mine", SettingSubnetID: "subnet-1"}
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(f.groups) != 0 {
		t.Error("a group was made although one was named")
	}
	launch := f.launches[0]
	if launch.Get("NetworkInterface.1.SecurityGroupId.1") != "sg-mine" || launch.Get("NetworkInterface.1.SubnetId") != "subnet-1" || launch.Get("NetworkInterface.1.AssociatePublicIpAddress") != "true" {
		t.Errorf("network interface = %v", launch)
	}
}

func TestARepeatedCreateIsTheSameLaunch(t *testing.T) {
	f, p := newFakeEC2(t)
	a, _ := p.Create(context.Background(), spec("burst-a"))
	b, _ := p.Create(context.Background(), spec("burst-a"))
	if a.ID != b.ID || len(f.instances) != 1 {
		t.Errorf("a repeated create made %d machines", len(f.instances))
	}
	if f.launches[0].Get("ClientToken") != f.launches[1].Get("ClientToken") {
		t.Error("the idempotency token must be the same for the same machine")
	}
}

func TestAMismatchedRepeatFindsTheMachineTheFirstTryMade(t *testing.T) {
	f, p := newFakeEC2(t)
	first, _ := p.Create(context.Background(), spec("burst-a"))
	f.fail["RunInstances"] = "IdempotentParameterMismatch"
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil || again.ID != first.ID {
		t.Fatalf("again = %+v, %v", again, err)
	}
}

func TestGetListAndDeleteWorkOnTheRegionInTheID(t *testing.T) {
	f, p := newFakeEC2(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	f.run("i-0002")

	got, err := p.Get(context.Background(), created.ID)
	if err != nil || !got.Ready() || got.PublicIPv4 != "203.0.113.9" || got.PrivateIPv4 != "10.0.0.9" || got.Region != "eu-north-1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if got.Labels[compute.LabelNode] != "burst-a" || got.Name != "burst-a" {
		t.Errorf("labels = %v name = %q", got.Labels, got.Name)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), "eu-north-1/i-9999"); err != nil {
		t.Errorf("deleting a machine that is gone: %v", err)
	}
	after, _ := p.Get(context.Background(), created.ID)
	if after.State != compute.StateGone {
		t.Errorf("a terminated machine is %s, want Gone", after.State)
	}
	if _, err := p.Get(context.Background(), "eu-north-1/i-9999"); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("an unknown machine: %v", err)
	}
}

func TestListFindsOnlyTheOwnersLiveMachinesAcrossPages(t *testing.T) {
	f, p := newFakeEC2(t)
	f.pageSize = 2
	for _, name := range []string{"a", "b", "c"} {
		if _, err := p.Create(context.Background(), spec("burst-"+name)); err != nil {
			t.Fatal(err)
		}
	}
	other := spec("web-a")
	other.Labels = compute.Owner{Cluster: "someone-else", Pool: "burst"}.Labels("web-a")
	if _, err := p.Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), "eu-north-1/i-0002"); err != nil {
		t.Fatal(err)
	}

	got, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("listed %d machines, want the two live ones of the owner", len(got))
	}
	for _, i := range got {
		if i.Labels[compute.LabelCluster] != "c1" || i.State == compute.StateGone {
			t.Errorf("listed %+v", i)
		}
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for code, check := range map[string]func(error) bool{
		"InstanceLimitExceeded":        compute.IsQuota,
		"VcpuLimitExceeded":            compute.IsQuota,
		"InsufficientInstanceCapacity": func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) },
		"UnauthorizedOperation":        func(err error) bool { return errors.Is(err, compute.ErrAuth) },
		"AuthFailure":                  func(err error) bool { return errors.Is(err, compute.ErrAuth) },
	} {
		t.Run(code, func(t *testing.T) {
			f, p := newFakeEC2(t)
			f.fail["RunInstances"] = code
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestVerifyNamesTheAccountAndWarnsWhenLaunchingIsNotAllowed(t *testing.T) {
	f, p := newFakeEC2(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "AWS account 123456789012" || len(account.Warnings) != 0 {
		t.Errorf("account = %+v", account)
	}
	f.dryRunResult = "UnauthorizedOperation"
	account, err = p.Verify(context.Background())
	if err != nil || len(account.Warnings) != 1 || !strings.Contains(account.Warnings[0], "may not launch") {
		t.Errorf("account = %+v, %v", account, err)
	}
}

func TestSizesAreSmallestFirstAndLeaveOutHugeOnes(t *testing.T) {
	_, p := newFakeEC2(t)
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0].ID != "t3.medium" || sizes[1].ID != "t3.large" || sizes[0].MemoryMiB != 4096 {
		t.Errorf("sizes = %+v", sizes)
	}
}

func TestStatesAreTranslated(t *testing.T) {
	for in, want := range map[string]compute.State{
		"pending": compute.StatePending, "running": compute.StateRunning, "stopped": compute.StateStopped,
		"shutting-down": compute.StateTerminating, "terminated": compute.StateGone,
	} {
		if got := stateOf(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestTheProviderRegistersWithTheStorageConnectionsCredentialKeys(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok {
		t.Fatal("not registered")
	}
	keys := map[string]bool{}
	for _, f := range d.Fields {
		keys[f.Key] = true
	}
	if !keys["accessKeyId"] || !keys["secretAccessKey"] {
		t.Errorf("fields = %+v", d.Fields)
	}
	if _, err := compute.New(ID, compute.Credentials{"accessKeyId": "a"}, compute.Options{}); err == nil {
		t.Error("a provider was built without the secret key")
	}
}

func controlPlaneGroup(f *fakeEC2) {
	f.groups["sg-cp"] = map[string]any{"name": "control-plane", "rules": []permission{{"tcp", 22, "198.51.100.1/32"}}}
}

func TestAMachinesAddressIsOpenedOnTheControlPlanesGroupAndClosedAgain(t *testing.T) {
	f, p := newFakeEC2(t)
	controlPlaneGroup(f)
	target := compute.AccessTarget{ID: "sg-cp", Region: "eu-north-1"}
	rules := []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.9/32"}, {Protocol: "udp", Port: 51820, CIDR: "203.0.113.9/32"}}

	if err := p.AllowAccess(context.Background(), target, rules); err != nil {
		t.Fatal(err)
	}
	got := f.groups["sg-cp"]["rules"].([]permission)
	if len(got) != 3 {
		t.Fatalf("rules = %v", got)
	}
	for _, d := range f.descriptions {
		if d != "geass" {
			t.Errorf("a rule Geass added is not marked as Geass's: %q", d)
		}
	}
	// The rule that was there stays untouched.
	if got[0] != (permission{"tcp", 22, "198.51.100.1/32"}) {
		t.Errorf("the control plane's own rule was changed: %v", got)
	}

	if err := p.RevokeAccess(context.Background(), target, rules); err != nil {
		t.Fatal(err)
	}
	got = f.groups["sg-cp"]["rules"].([]permission)
	if len(got) != 1 || got[0].Port != 22 {
		t.Errorf("after revoking: %v", got)
	}
}

func TestOpeningAddressesThatAreAlreadyOpenIsNotAnErrorAndStillOpensTheRest(t *testing.T) {
	f, p := newFakeEC2(t)
	controlPlaneGroup(f)
	target := compute.AccessTarget{ID: "sg-cp", Region: "eu-north-1"}
	if err := p.AllowAccess(context.Background(), target, []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.9/32"}}); err != nil {
		t.Fatal(err)
	}
	// One of these two is there already: EC2 refuses the pair, so each is tried alone.
	err := p.AllowAccess(context.Background(), target, []compute.AccessRule{
		{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.9/32"}, {Protocol: "udp", Port: 51820, CIDR: "203.0.113.9/32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.groups["sg-cp"]["rules"].([]permission); len(got) != 3 {
		t.Errorf("rules = %v, want the new one added", got)
	}
	if err := p.RevokeAccess(context.Background(), target, []compute.AccessRule{{Protocol: "tcp", Port: 6443, CIDR: "203.0.113.200/32"}}); err != nil {
		t.Errorf("revoking a rule that is not there: %v", err)
	}
}

// EC2 appends about fifteen hundred characters of encoded text to a refusal. The sentence before it says what was
// refused and by which policy, which is all a person can use, so it is the message.
func TestARefusalKeepsItsReasonAndLosesTheEncodedBlob(t *testing.T) {
	reason := "You are not authorized to perform this operation. User: arn:aws:iam::123456789012:user/geass is not authorized to perform: ec2:RunInstances on resource: arn:aws:ec2:eu-north-1:123456789012:instance/* with an explicit deny in a service control policy: arn:aws:organizations::111111111111:policy/o-abc/service_control_policy/p-xyz."
	blob := " Encoded authorization failure message: " + strings.Repeat("93B4d2qEiqhOnd62So", 80)

	for name, body := range map[string]string{
		"EC2": `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>` + reason + blob + `</Message></Error></Errors></Response>`,
		"STS": `<ErrorResponse><Error><Code>UnauthorizedOperation</Code><Message>` + reason + blob + `</Message></Error></ErrorResponse>`,
	} {
		code, message := parseError([]byte(body))
		if code != "UnauthorizedOperation" {
			t.Fatalf("%s: code %q", name, code)
		}
		if message != reason {
			t.Fatalf("%s: message %q, want only the reason", name, message)
		}
	}

	_, plain := parseError([]byte(`<Response><Errors><Error><Code>InvalidParameterValue</Code><Message>Value (x) for parameter instanceType is invalid.</Message></Error></Errors></Response>`))
	if plain != "Value (x) for parameter instanceType is invalid." {
		t.Fatalf("a message without the blob is left alone: %q", plain)
	}
	err := classify("POST", "/", 403, []byte(`<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>`+reason+blob+`</Message></Error></Errors></Response>`))
	if !errors.Is(err, compute.ErrAuth) || strings.Contains(err.Error(), "Encoded") || !strings.Contains(err.Error(), "service control policy") {
		t.Fatalf("a refusal is an auth error that names the policy and not the blob: %v", err)
	}
}

// One Provider serves several controllers and the dashboard at once, and it builds a client the first time a service
// and region are used. Run with -race, this fails if the cache is not guarded.
func TestTheClientCacheIsSafeForConcurrentUse(t *testing.T) {
	p := New("key", "secret", "eu-north-1", compute.Options{})
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			p.api("ec2", fmt.Sprintf("region-%d", i%4))
			p.api("sts", "eu-north-1")
		})
	}
	wg.Wait()
	if got := p.api("ec2", "region-0"); got == nil || p.api("ec2", "region-0") != got {
		t.Fatal("a client is made once and then reused")
	}
}

// EC2 answers a repeated token with the instance it launched, even after that instance is terminated, so a node that
// reuses the name of a removed one must send another token.
func TestTheLaunchTokenFollowsTheNodeAndNotOnlyItsName(t *testing.T) {
	spec := compute.InstanceSpec{Name: "web-1", Labels: map[string]string{compute.LabelCluster: "c1"}, Incarnation: "uid-1"}
	same := spec
	if clientToken(spec) != clientToken(same) {
		t.Fatal("a repeated create of one node is the same launch")
	}
	again := spec
	again.Incarnation = "uid-2"
	if clientToken(spec) == clientToken(again) {
		t.Fatal("a new node with the same name must not get the old node's launch back")
	}
}
