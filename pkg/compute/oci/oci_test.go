package oci

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/degoke/geass/pkg/compute"
)

// docPrivateKey and docPublicKey are the test key pair published in Oracle's request signing documentation
// (https://docs.oracle.com/en-us/iaas/Content/API/Concepts/signingrequests.htm). Oracle says to use them for tests only.
const docPrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXgIBAAKBgQDCFENGw33yGihy92pDjZQhl0C36rPJj+CvfSC8+q28hxA161QF
NUd13wuCTUcq0Qd2qsBe/2hFyc2DCJJg0h1L78+6Z4UMR7EOcpfdUE9Hf3m/hs+F
UR45uBJeDK1HSFHD8bHKD6kv8FPGfJTotc+2xjJwoYi+1hqp1fIekaxsyQIDAQAB
AoGBAJR8ZkCUvx5kzv+utdl7T5MnordT1TvoXXJGXK7ZZ+UuvMNUCdN2QPc4sBiA
QWvLw1cSKt5DsKZ8UETpYPy8pPYnnDEz2dDYiaew9+xEpubyeW2oH4Zx71wqBtOK
kqwrXa/pzdpiucRRjk6vE6YY7EBBs/g7uanVpGibOVAEsqH1AkEA7DkjVH28WDUg
f1nqvfn2Kj6CT7nIcE3jGJsZZ7zlZmBmHFDONMLUrXR/Zm3pR5m0tCmBqa5RK95u
412jt1dPIwJBANJT3v8pnkth48bQo/fKel6uEYyboRtA5/uHuHkZ6FQF7OUkGogc
mSJluOdc5t6hI1VsLn0QZEjQZMEOWr+wKSMCQQCC4kXJEsHAve77oP6HtG/IiEn7
kpyUXRNvFsDE0czpJJBvL/aRFUJxuRK91jhjC68sA7NsKMGg5OXb5I5Jj36xAkEA
gIT7aFOYBFwGgQAQkWNKLvySgKbAZRTeLBacpHMuQdl1DfdntvAyqpAZ0lY0RKmW
G6aFKaqQfOXKCyWoUiVknQJAXrlgySFci/2ueKlIE1QqIiLSZ8V8OlpFLRnb1pzI
7U1yQXnTAEFYM560yJlzUpOb1V4cScGd365tiSMvxLOvTA==
-----END RSA PRIVATE KEY-----
`

const docPublicKey = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDCFENGw33yGihy92pDjZQhl0C3
6rPJj+CvfSC8+q28hxA161QFNUd13wuCTUcq0Qd2qsBe/2hFyc2DCJJg0h1L78+6
Z4UMR7EOcpfdUE9Hf3m/hs+FUR45uBJeDK1HSFHD8bHKD6kv8FPGfJTotc+2xjJw
oYi+1hqp1fIekaxsyQIDAQAB
-----END PUBLIC KEY-----
`

const (
	testTenancy     = "ocid1.tenancy.oc1..tenancyuniqueid"
	testUser        = "ocid1.user.oc1..useruniqueid"
	testFingerprint = "20:3b:97:13:55:1c:5b:0d:d3:37:d8:50:4e:c5:3a:34"
	testCompartment = "ocid1.compartment.oc1..compartmentuniqueid"
	testRegion      = "eu-frankfurt-1"
)

func testCredentials() compute.Credentials {
	return compute.Credentials{
		keyTenancy: testTenancy, keyUser: testUser, keyFingerprint: testFingerprint,
		keyPrivateKey: docPrivateKey, keyCompartment: testCompartment,
	}
}

func docPublic(t *testing.T) *rsa.PublicKey {
	t.Helper()
	block, _ := pem.Decode([]byte(docPublicKey))
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.(*rsa.PublicKey)
}

// The request of the documentation's GET example, its date, and the signing string the documentation shows for it.
// The expected signature was computed outside Go:
//
//	openssl dgst -sha256 -sign docPrivateKey signingString | base64
const (
	docGetURL = "https://iaas.us-phoenix-1.oraclecloud.com/20160918/instances?availabilityDomain=Pjwf%3A%20PHX-AD-1" +
		"&compartmentId=ocid1.compartment.oc1..aaaaaaaam3we6vgnherjq5q2idnccdflvjsnog7mlr6rtdb25gilchfeyjxa" +
		"&displayName=TeamXInstances&volumeId=ocid1.volume.oc1.phx.abyhqljrgvttnlx73nmrwfaux7kcvzfs3s66izvxf2h4lgvyndsdsnoiwr5q"
	docGetSigningString = "date: Thu, 05 Jan 2014 21:31:40 GMT\n" +
		"(request-target): get /20160918/instances?availabilityDomain=Pjwf%3A%20PHX-AD-1" +
		"&compartmentId=ocid1.compartment.oc1..aaaaaaaam3we6vgnherjq5q2idnccdflvjsnog7mlr6rtdb25gilchfeyjxa" +
		"&displayName=TeamXInstances&volumeId=ocid1.volume.oc1.phx.abyhqljrgvttnlx73nmrwfaux7kcvzfs3s66izvxf2h4lgvyndsdsnoiwr5q\n" +
		"host: iaas.us-phoenix-1.oraclecloud.com"
	docGetSignature = "GBas7grhyrhSKHP6AVIj/h5/Vp8bd/peM79H9Wv8kjoaCivujVXlpbKLjMPeDUhxkFIWtTtLBj3sUzaFj34XE6YZAHc9r2DmE4pMwOAy/kiITcZxa1oHPOeRheC0jP2dqbTll8fmTZVwKZOKHYPtrLJIJQHJjNvxFWeHQjMaR7M="

	// The documentation's POST example, with a body of our own (the documentation's is elided): its length, its SHA-256
	// (openssl dgst -sha256 -binary | base64) and the signature over the signing string, all computed outside Go.
	docPostURL         = "https://iaas.us-phoenix-1.oraclecloud.com/20160918/volumeAttachments"
	docPostBody        = `{"compartmentId":"ocid1.compartment.oc1..aaaaaaaam3we6vgnherjq5q2idnccdflvjsnog7mlr6rtdb25gilchfeyjxa","instanceId":"ocid1.instance.oc1.phx.abuw4ljrlsfiqw6vzy4qlbvy7vlo7lozywphw7iaugwb4r7dtvifcvhjnoyqx","volumeId":"ocid1.volume.oc1.phx.abyhqljrgvttnlx73nmrwfaux7kcvzfs3s66izvxf2h4lgvyndsdsnoiwr5q"}`
	docPostSHA         = "pa7c2ZZ/IV6mi6AEbm20QZcuJN+0CSZSQ1OVumuH5No="
	docPostSigningText = "date: Thu, 05 Jan 2014 21:31:40 GMT\n(request-target): post /20160918/volumeAttachments\nhost: iaas.us-phoenix-1.oraclecloud.com\n" +
		"content-length: 298\ncontent-type: application/json\nx-content-sha256: pa7c2ZZ/IV6mi6AEbm20QZcuJN+0CSZSQ1OVumuH5No="
	docPostSignature = "CH/Y9b0b6s/v+PIYSNvh68VsozTq7+YbvFUHe9qXZ5fKdlWo7opsy9q4HGqA3JsbXAtOAwH8oXh8kwVVdzV5Q6rDsa4m7rJg++4PAXqhvJYmBik+4XuyEqCbb98w06ctO01b88n0QX5IeEc1XItUOweoTq3kmhgOtTS9aQJRM6w="
)

// docDate is the Date the documentation's examples carry. It names a Thursday that 5 January 2014 was not (it was a
// Sunday), so no clock can produce it: the test puts it on the request, which the signer then signs as it is.
const docDate = "Thu, 05 Jan 2014 21:31:40 GMT"

func signer(t *testing.T) *Provider {
	t.Helper()
	p, err := New(testCredentials(), compute.Options{Region: testRegion})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func signatureParams(header string) map[string]string {
	params := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+)="([^"]*)"`).FindAllStringSubmatch(header, -1) {
		params[m[1]] = m[2]
	}
	return params
}

func TestSigningAGETMatchesTheDocumentationsExample(t *testing.T) {
	p := signer(t)
	req, err := http.NewRequest(http.MethodGet, docGetURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Date", docDate)
	if err := p.sign(req, nil); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Date"); got != docDate {
		t.Errorf("Date = %q", got)
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, `Signature version="1",headers="date (request-target) host",keyId="`+testTenancy+"/"+testUser+"/"+testFingerprint+`",algorithm="rsa-sha256",signature="`) {
		t.Errorf("Authorization = %s", auth)
	}
	params := signatureParams(auth)
	// The signature is the one computed outside Go over the documentation's own signing string.
	if params["signature"] != docGetSignature {
		t.Errorf("signature = %s\nwant      %s", params["signature"], docGetSignature)
	}
	// And it verifies, under the documentation's public key, over the documentation's own signing string.
	raw, _ := base64.StdEncoding.DecodeString(params["signature"])
	digest := sha256.Sum256([]byte(docGetSigningString))
	if err := rsa.VerifyPKCS1v15(docPublic(t), crypto.SHA256, digest[:], raw); err != nil {
		t.Errorf("the signature does not verify over the documented signing string: %v", err)
	}
	if req.Header.Get("x-content-sha256") != "" {
		t.Error("a GET carries body headers")
	}
}

func TestSigningAPOSTMatchesAnIndependentComputation(t *testing.T) {
	p := signer(t)
	req, err := http.NewRequest(http.MethodPost, docPostURL, strings.NewReader(docPostBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Date", docDate)
	if err := p.sign(req, []byte(docPostBody)); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("x-content-sha256"); got != docPostSHA {
		t.Errorf("x-content-sha256 = %s, want %s", got, docPostSHA)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", req.Header.Get("Content-Type"))
	}
	params := signatureParams(req.Header.Get("Authorization"))
	if params["headers"] != "date (request-target) host content-length content-type x-content-sha256" {
		t.Errorf("headers = %q", params["headers"])
	}
	if params["signature"] != docPostSignature {
		t.Errorf("signature = %s\nwant      %s", params["signature"], docPostSignature)
	}
	raw, _ := base64.StdEncoding.DecodeString(params["signature"])
	digest := sha256.Sum256([]byte(docPostSigningText))
	if err := rsa.VerifyPKCS1v15(docPublic(t), crypto.SHA256, digest[:], raw); err != nil {
		t.Errorf("the signature does not verify over the signing string: %v", err)
	}
}

func TestWithoutADateTheSignerUsesTheClock(t *testing.T) {
	p := signer(t)
	p.now = func() time.Time { return time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC) }
	req, _ := http.NewRequest(http.MethodGet, docGetURL, nil)
	if err := p.sign(req, nil); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Date"); got != "Mon, 19 Oct 2026 10:00:00 GMT" {
		t.Errorf("Date = %q", got)
	}
}

func TestADELETEIsSignedLikeAGETAndAnEmptyPOSTStillSignsItsBodyHeaders(t *testing.T) {
	p := signer(t)
	del, _ := http.NewRequest(http.MethodDelete, "https://iaas.eu-frankfurt-1.oraclecloud.com/20160918/instances/ocid1.instance.oc1.eu-frankfurt-1.x?preserveBootVolume=false", nil)
	if err := p.sign(del, nil); err != nil {
		t.Fatal(err)
	}
	if got := signatureParams(del.Header.Get("Authorization"))["headers"]; got != "date (request-target) host" {
		t.Errorf("a DELETE signs %q", got)
	}
	post, _ := http.NewRequest(http.MethodPost, docPostURL, nil)
	if err := p.sign(post, nil); err != nil {
		t.Fatal(err)
	}
	if got := signatureParams(post.Header.Get("Authorization"))["headers"]; got != "date (request-target) host content-length content-type x-content-sha256" {
		t.Errorf("an empty POST signs %q: Oracle wants the body headers even then", got)
	}
	if post.Header.Get("x-content-sha256") != "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=" {
		t.Errorf("the SHA-256 of an empty body = %s", post.Header.Get("x-content-sha256"))
	}
}

// fakeOCI is the part of Oracle's Compute, Networking and Identity APIs that Geass uses, in memory. It checks the
// signature of every request on its own, rebuilding the signing string from what it received.
type fakeOCI struct {
	t   *testing.T
	pub *rsa.PublicKey

	mu          sync.Mutex
	instances   map[string]map[string]any
	order       []string
	byToken     map[string]string
	vnicOf      map[string]string
	vnics       map[string]map[string]any
	next        int
	launches    int
	requests    []string
	badRequests []string

	domains []string
	// noCapacityIn are availability domains that answer a launch with "Out of host capacity".
	noCapacityIn map[string]bool
	// failLaunch, when set, is the status and body of the next launch.
	failLaunch func() (int, map[string]any)
	pageSize   int
	images     []map[string]any
	// noCompartmentAccess makes the compartment's own record unreadable, and domainsForbidden the availability domains.
	noCompartmentAccess bool
	domainsForbidden    bool
	lastImageQuery      string
	lastLaunch          map[string]any
	launchHeaders       []http.Header
	listInstancesRaw    string
}

func newFake(t *testing.T) (*fakeOCI, *Provider) {
	f := &fakeOCI{
		t: t, pub: docPublic(t), instances: map[string]map[string]any{}, byToken: map[string]string{},
		vnicOf: map[string]string{}, vnics: map[string]map[string]any{}, pageSize: 100,
		domains: []string{"Uocm:EU-FRANKFURT-1-AD-1", "Uocm:EU-FRANKFURT-1-AD-2", "Uocm:EU-FRANKFURT-1-AD-3"}, noCapacityIn: map[string]bool{},
		images: []map[string]any{
			{"id": "ocid1.image.oc1.eu-frankfurt-1.newest", "lifecycleState": "AVAILABLE", "operatingSystem": "Canonical Ubuntu"},
			{"id": "ocid1.image.oc1.eu-frankfurt-1.older", "lifecycleState": "AVAILABLE", "operatingSystem": "Canonical Ubuntu"},
		},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	p, err := New(testCredentials(), compute.Options{BaseURL: srv.URL, Region: testRegion})
	if err != nil {
		t.Fatal(err)
	}
	p.sleep = func(context.Context, time.Duration) error { return nil }
	return f, p
}

func (f *fakeOCI) reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeOCI) fail(w http.ResponseWriter, status int, code, message string) {
	f.reply(w, status, map[string]string{"code": code, "message": message})
}

// verify rebuilds the signing string from the request as received and checks the signature against the public key.
func (f *fakeOCI) verify(r *http.Request, body []byte) error {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Signature ") {
		return errors.New("no Signature authorization")
	}
	params := signatureParams(auth)
	if params["version"] != "1" || params["algorithm"] != "rsa-sha256" || params["keyId"] != testTenancy+"/"+testUser+"/"+testFingerprint {
		return fmt.Errorf("bad parameters: %v", params)
	}
	names := strings.Fields(params["headers"])
	have := map[string]bool{}
	var lines []string
	for _, name := range names {
		have[name] = true
		switch name {
		case "(request-target)":
			lines = append(lines, "(request-target): "+strings.ToLower(r.Method)+" "+r.URL.RequestURI())
		case "host":
			lines = append(lines, "host: "+r.Host)
		case "content-length":
			if r.Header.Get("Content-Length") != strconv.Itoa(len(body)) && r.ContentLength != int64(len(body)) {
				return fmt.Errorf("content-length %q for a body of %d", r.Header.Get("Content-Length"), len(body))
			}
			lines = append(lines, "content-length: "+strconv.Itoa(len(body)))
		case "x-content-sha256":
			sum := sha256.Sum256(body)
			if got := r.Header.Get(name); got != base64.StdEncoding.EncodeToString(sum[:]) {
				return fmt.Errorf("x-content-sha256 %q does not match the body", got)
			}
			lines = append(lines, name+": "+r.Header.Get(name))
		default:
			lines = append(lines, name+": "+r.Header.Get(name))
		}
	}
	required := []string{"(request-target)", "host", "date"}
	if r.Method == "POST" || r.Method == "PUT" {
		required = append(required, "x-content-sha256", "content-type", "content-length")
	}
	for _, name := range required {
		if !have[name] {
			return fmt.Errorf("header %s is not signed", name)
		}
	}
	date, err := http.ParseTime(r.Header.Get("Date"))
	if err != nil {
		return fmt.Errorf("Date: %w", err)
	}
	if skew := time.Since(date); skew > 5*time.Minute || skew < -5*time.Minute {
		return fmt.Errorf("the clock is off by %s", skew)
	}
	signature, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return rsa.VerifyPKCS1v15(f.pub, crypto.SHA256, digest[:], signature)
}

func (f *fakeOCI) paginate(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	start := 0
	if page := r.URL.Query().Get("page"); page != "" {
		start, _ = strconv.Atoi(page)
	}
	end := min(start+f.pageSize, len(items))
	if end < len(items) {
		w.Header().Set("opc-next-page", strconv.Itoa(end))
	}
	if items == nil {
		items = []map[string]any{}
	}
	f.reply(w, 200, items[start:end])
}

func (f *fakeOCI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if err := f.verify(r, body); err != nil {
		f.badRequests = append(f.badRequests, r.Method+" "+r.URL.RequestURI()+": "+err.Error())
		f.fail(w, 401, "NotAuthenticated", "The required information to complete authentication was not provided or was incorrect.")
		return
	}
	const prefix = "/20160918/"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	switch {
	case r.Method == "GET" && path == "availabilityDomains":
		if f.domainsForbidden {
			f.fail(w, 404, "NotAuthorizedOrNotFound", "Authorization failed or requested resource not found.")
			return
		}
		var out []map[string]any
		for _, d := range f.domains {
			out = append(out, map[string]any{"name": d})
		}
		f.reply(w, 200, out)
	case r.Method == "GET" && strings.HasPrefix(path, "compartments/"):
		if f.noCompartmentAccess {
			f.fail(w, 404, "NotAuthorizedOrNotFound", "Authorization failed or requested resource not found.")
			return
		}
		f.reply(w, 200, map[string]any{"id": strings.TrimPrefix(path, "compartments/"), "name": "geass-compartment"})
	case r.Method == "GET" && path == "images":
		f.lastImageQuery = r.URL.RawQuery
		f.reply(w, 200, f.images)
	case r.Method == "GET" && path == "shapes":
		f.paginate(w, r, f.shapes())
	case r.Method == "POST" && path == "instances":
		f.launch(w, r, body)
	case r.Method == "GET" && path == "instances":
		f.listInstancesRaw = r.URL.RawQuery
		var all []map[string]any
		for _, id := range f.order {
			all = append(all, f.instances[id])
		}
		f.paginate(w, r, all)
	case strings.HasPrefix(path, "instances/"):
		f.instance(w, r, strings.TrimPrefix(path, "instances/"))
	case r.Method == "GET" && path == "vnicAttachments":
		var all []map[string]any
		for _, id := range f.order {
			if vnic, ok := f.vnicOf[id]; ok && (r.URL.Query().Get("instanceId") == "" || r.URL.Query().Get("instanceId") == id) {
				all = append(all, map[string]any{"instanceId": id, "lifecycleState": "ATTACHED", "vnicId": vnic, "nicIndex": 0})
			}
		}
		f.paginate(w, r, all)
	case r.Method == "GET" && strings.HasPrefix(path, "vnics/"):
		vnic, ok := f.vnics[strings.TrimPrefix(path, "vnics/")]
		if !ok {
			f.fail(w, 404, "NotAuthorizedOrNotFound", "vnic not found")
			return
		}
		f.reply(w, 200, vnic)
	default:
		f.fail(w, 404, "NotFound", "no route "+r.Method+" "+r.URL.Path)
	}
}

func (f *fakeOCI) launch(w http.ResponseWriter, r *http.Request, body []byte) {
	f.launches++
	f.launchHeaders = append(f.launchHeaders, r.Header.Clone())
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.lastLaunch = req
	if f.failLaunch != nil {
		status, out := f.failLaunch()
		f.reply(w, status, out)
		return
	}
	if f.noCapacityIn[req["availabilityDomain"].(string)] {
		f.fail(w, 500, "InternalError", "Out of host capacity.")
		return
	}
	token := r.Header.Get("opc-retry-token")
	if id, ok := f.byToken[token]; ok && token != "" {
		f.reply(w, 200, f.instances[id])
		return
	}
	f.next++
	id := fmt.Sprintf("ocid1.instance.oc1.%s.unique%d", testRegion, f.next)
	inst := map[string]any{
		"id": id, "displayName": req["displayName"], "lifecycleState": "PROVISIONING", "shape": req["shape"],
		"availabilityDomain": req["availabilityDomain"], "compartmentId": req["compartmentId"], "region": testRegion,
		"timeCreated": "2026-10-19T10:00:00.123Z", "freeformTags": req["freeformTags"],
	}
	if cfg, ok := req["shapeConfig"]; ok {
		inst["shapeConfig"] = cfg
	}
	f.instances[id] = inst
	f.order = append(f.order, id)
	f.byToken[token] = id
	f.reply(w, 200, inst)
}

func (f *fakeOCI) instance(w http.ResponseWriter, r *http.Request, id string) {
	inst, ok := f.instances[id]
	if !ok {
		f.fail(w, 404, "NotAuthorizedOrNotFound", "instance not found")
		return
	}
	if r.Method == "DELETE" {
		if inst["lifecycleState"] == "TERMINATING" || inst["lifecycleState"] == "TERMINATED" {
			f.fail(w, 409, "IncorrectState", "instance is being terminated")
			return
		}
		if r.URL.Query().Get("preserveBootVolume") != "false" {
			f.fail(w, 400, "InvalidParameter", "preserveBootVolume must be false: the boot volume would be left behind")
			return
		}
		inst["lifecycleState"] = "TERMINATING"
		w.WriteHeader(204)
		return
	}
	f.reply(w, 200, inst)
}

func (f *fakeOCI) shapes() []map[string]any {
	return []map[string]any{
		{"shape": "VM.Standard.E4.Flex", "processorDescription": "2.55 GHz AMD EPYC 7J13", "ocpus": 1, "memoryInGBs": 16, "isFlexible": true,
			"ocpuOptions": map[string]any{"min": 1, "max": 64}, "memoryOptions": map[string]any{"minInGBs": 1, "maxInGBs": 1024, "minPerOcpuInGBs": 1, "maxPerOcpuInGBs": 64, "defaultPerOcpuInGBs": 16}},
		{"shape": "VM.Standard.A1.Flex", "processorDescription": "3.0 GHz Ampere Altra", "ocpus": 1, "memoryInGBs": 6, "isFlexible": true,
			"ocpuOptions": map[string]any{"min": 1, "max": 80}, "memoryOptions": map[string]any{"minInGBs": 1, "maxInGBs": 512, "minPerOcpuInGBs": 1, "maxPerOcpuInGBs": 64, "defaultPerOcpuInGBs": 6}},
		{"shape": "VM.Standard2.2", "processorDescription": "2.0 GHz Intel Xeon Platinum 8167M", "ocpus": 2, "memoryInGBs": 30},
		{"shape": "VM.Standard2.2", "processorDescription": "duplicate listed per availability domain", "ocpus": 2, "memoryInGBs": 30},
		{"shape": "BM.Standard.E4.128", "ocpus": 128, "memoryInGBs": 2048},
		{"shape": "VM.GPU.A10.1", "ocpus": 15, "memoryInGBs": 240, "gpus": 1},
		{"shape": "VM.DenseIO.E4.Flex", "ocpus": 8, "memoryInGBs": 128, "localDisks": 1, "isFlexible": true},
	}
}

// run makes an instance running with a VNIC that has the addresses.
func (f *fakeOCI) run(id, publicIP string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[id]["lifecycleState"] = "RUNNING"
	vnic := "ocid1.vnic.oc1.eu-frankfurt-1." + id[strings.LastIndex(id, ".")+1:]
	f.vnicOf[id] = vnic
	f.vnics[vnic] = map[string]any{"id": vnic, "publicIp": publicIP, "privateIp": "10.0.0.7"}
}

func (f *fakeOCI) state(id, lifecycle string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.instances[id]["lifecycleState"] = lifecycle
}

func (f *fakeOCI) noBadSignatures() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.badRequests) > 0 {
		f.t.Errorf("requests with a signature Oracle would refuse: %v", f.badRequests)
	}
}

func spec(name string) compute.InstanceSpec {
	owner := compute.Owner{Cluster: "c1", Pool: "burst"}
	return compute.InstanceSpec{
		Name: name, Region: testRegion, Size: "VM.Standard.E4.Flex:2:8", UserData: "#cloud-config\nhostname: x\n",
		Labels:   owner.Labels(name),
		Settings: map[string]string{SettingSubnetID: "ocid1.subnet.oc1.eu-frankfurt-1.sub"},
	}
}

func TestEveryRequestIsSignedTheWayOracleChecks(t *testing.T) {
	f, p := newFake(t)
	created, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.run(created.ID[strings.Index(created.ID, "/")+1:], "130.61.0.9")
	if _, err := p.Get(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.List(context.Background(), compute.Owner{Cluster: "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Sizes(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	f.noBadSignatures()
	if len(f.requests) < 8 {
		t.Errorf("only %d requests were made: %v", len(f.requests), f.requests)
	}
}

func TestACredentialThatCannotWorkIsRefusedWhenTheProviderIsBuilt(t *testing.T) {
	with := func(key, value string) compute.Credentials {
		c := testCredentials()
		c[key] = value
		return c
	}
	for name, creds := range map[string]compute.Credentials{
		"tenancy typo":       with(keyTenancy, "ocid1.user.oc1..x"),
		"user typo":          with(keyUser, "ocid1.tenancy.oc1..x"),
		"compartment typo":   with(keyCompartment, "ocid1.subnet.oc1..x"),
		"ssh style print":    with(keyFingerprint, "SHA256:abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"),
		"not pem":            with(keyPrivateKey, "nope"),
		"passphrase":         with(keyPrivateKey, "-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n"),
		"legacy passphrase":  with(keyPrivateKey, "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,00\n\nAAAA\n-----END RSA PRIVATE KEY-----\n"),
		"garbage in the pem": with(keyPrivateKey, "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(creds, compute.Options{}); err == nil {
				t.Error("credentials that cannot sign were accepted")
			}
		})
	}
	if _, err := New(with(keyCompartment, testTenancy), compute.Options{}); err != nil {
		t.Errorf("the root compartment (the tenancy) was refused: %v", err)
	}
}

func TestCreateSendsTheMachineLabelledWithItsUserDataAndAPublicAddress(t *testing.T) {
	f, p := newFake(t)
	instance, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	f.noBadSignatures()
	if !strings.HasPrefix(instance.ID, "eu-frankfurt-1/ocid1.instance.") || instance.Name != "burst-a" || instance.State != compute.StatePending || instance.PublicIPv4 != "" {
		t.Errorf("instance = %+v", instance)
	}
	if instance.Size != "VM.Standard.E4.Flex:2:8" || instance.Region != testRegion {
		t.Errorf("size/region = %s %s", instance.Size, instance.Region)
	}
	sent := f.lastLaunch
	if sent["compartmentId"] != testCompartment || sent["availabilityDomain"] != "Uocm:EU-FRANKFURT-1-AD-1" || sent["shape"] != "VM.Standard.E4.Flex" || sent["displayName"] != "burst-a" {
		t.Errorf("launch = %v", sent)
	}
	if cfg := sent["shapeConfig"].(map[string]any); cfg["ocpus"] != float64(2) || cfg["memoryInGBs"] != float64(8) {
		t.Errorf("shapeConfig = %v", cfg)
	}
	source := sent["sourceDetails"].(map[string]any)
	if source["sourceType"] != "image" || source["imageId"] != "ocid1.image.oc1.eu-frankfurt-1.newest" {
		t.Errorf("sourceDetails = %v", source)
	}
	vnic := sent["createVnicDetails"].(map[string]any)
	if vnic["subnetId"] != "ocid1.subnet.oc1.eu-frankfurt-1.sub" || vnic["assignPublicIp"] != true {
		t.Errorf("createVnicDetails = %v", vnic)
	}
	if _, set := vnic["nsgIds"]; set {
		t.Error("an NSG was sent that nobody asked for")
	}
	userData, _ := base64.StdEncoding.DecodeString(sent["metadata"].(map[string]any)["user_data"].(string))
	if string(userData) != "#cloud-config\nhostname: x\n" {
		t.Errorf("user_data = %q, want the cloud-init document untouched, base64 encoded", userData)
	}
	tags := sent["freeformTags"].(map[string]any)
	if tags[compute.LabelManaged] != "true" || tags[compute.LabelCluster] != "c1" || tags[compute.LabelPool] != "burst" || tags[compute.LabelNode] != "burst-a" {
		t.Errorf("freeformTags = %v", tags)
	}
	if _, set := sent["preemptibleInstanceConfig"]; set {
		t.Error("an on-demand machine was made preemptible")
	}
	if token := f.launchHeaders[0].Get("opc-retry-token"); !strings.HasPrefix(token, "geass-") || len(token) > 64 {
		t.Errorf("opc-retry-token = %q", token)
	}
	if !strings.Contains(f.lastImageQuery, "operatingSystem=Canonical%20Ubuntu") || !strings.Contains(f.lastImageQuery, "operatingSystemVersion=24.04") ||
		!strings.Contains(f.lastImageQuery, "shape=VM.Standard.E4.Flex") || !strings.Contains(f.lastImageQuery, "sortBy=TIMECREATED") || strings.Contains(f.lastImageQuery, "+") {
		t.Errorf("image query = %s", f.lastImageQuery)
	}
}

func TestAFixedShapeSendsNoShapeConfigAndASpotMachineIsTerminatedWhenTakenBack(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Size = "VM.Standard2.2"
	s.Spot = true
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if _, set := f.lastLaunch["shapeConfig"]; set {
		t.Error("a fixed shape was given a shapeConfig")
	}
	action := f.lastLaunch["preemptibleInstanceConfig"].(map[string]any)["preemptionAction"].(map[string]any)
	if action["type"] != "TERMINATE" || action["preserveBootVolume"] != false {
		t.Errorf("preemptionAction = %v", action)
	}
}

func TestTheImageNSGsDomainAndDiskCanBeChosen(t *testing.T) {
	f, p := newFake(t)
	s := spec("burst-a")
	s.Settings[SettingImageID] = "ocid1.image.oc1.eu-frankfurt-1.mine"
	s.Settings[SettingNSGID] = "ocid1.networksecuritygroup.oc1..a, ocid1.networksecuritygroup.oc1..b"
	s.Settings[SettingAvailabilityDomain] = "Uocm:EU-FRANKFURT-1-AD-2"
	s.Settings[SettingDiskSizeGB] = "120"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	source := f.lastLaunch["sourceDetails"].(map[string]any)
	vnic := f.lastLaunch["createVnicDetails"].(map[string]any)
	if source["imageId"] != "ocid1.image.oc1.eu-frankfurt-1.mine" || source["bootVolumeSizeInGBs"] != float64(120) {
		t.Errorf("sourceDetails = %v", source)
	}
	if fmt.Sprint(vnic["nsgIds"]) != "[ocid1.networksecuritygroup.oc1..a ocid1.networksecuritygroup.oc1..b]" {
		t.Errorf("nsgIds = %v", vnic["nsgIds"])
	}
	if f.lastLaunch["availabilityDomain"] != "Uocm:EU-FRANKFURT-1-AD-2" {
		t.Errorf("availabilityDomain = %v", f.lastLaunch["availabilityDomain"])
	}
	for _, r := range f.requests {
		if strings.Contains(r, "availabilityDomains") || strings.HasSuffix(r, "/images") {
			t.Errorf("a lookup was made although the setting answers it: %s", r)
		}
	}
	// A machine's own image (an OCID) is used when the setting is not given.
	s2 := spec("burst-b")
	s2.Image = "ocid1.image.oc1.eu-frankfurt-1.fromspec"
	if _, err := p.Create(context.Background(), s2); err != nil {
		t.Fatal(err)
	}
	if got := f.lastLaunch["sourceDetails"].(map[string]any)["imageId"]; got != "ocid1.image.oc1.eu-frankfurt-1.fromspec" {
		t.Errorf("imageId = %v", got)
	}
}

func TestASubnetIsRequiredAndAFlexShapeNeedsItsSize(t *testing.T) {
	_, p := newFake(t)
	noSubnet := spec("burst-a")
	noSubnet.Settings = nil
	if _, err := p.Create(context.Background(), noSubnet); err == nil || !strings.Contains(err.Error(), SettingSubnetID) {
		t.Errorf("err = %v", err)
	}
	for _, size := range []string{"VM.Standard.E4.Flex", "VM.Standard.E4.Flex:two:8", "a:b", "VM.Standard.E4.Flex:0:8"} {
		s := spec("burst-a")
		s.Size = size
		if _, err := p.Create(context.Background(), s); err == nil {
			t.Errorf("size %q was accepted", size)
		}
	}
	d, _ := compute.Lookup(ID)
	if missing := d.MissingSettings(nil); len(missing) != 1 || missing[0] != SettingSubnetID {
		t.Errorf("required settings = %v", missing)
	}
}

func TestARepeatedCreateIsAnsweredWithTheSameMachine(t *testing.T) {
	f, p := newFake(t)
	first, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || len(f.instances) != 1 {
		t.Errorf("the repeat made a second machine: %s vs %s, %d instances", again.ID, first.ID, len(f.instances))
	}
	if f.launchHeaders[0].Get("opc-retry-token") != f.launchHeaders[1].Get("opc-retry-token") {
		t.Error("the retry token changed between the two creates")
	}
	other, _ := p.Create(context.Background(), spec("burst-b"))
	if other.ID == first.ID || f.launchHeaders[2].Get("opc-retry-token") == f.launchHeaders[0].Get("opc-retry-token") {
		t.Error("two machines share a retry token")
	}
	f.noBadSignatures()
}

func TestCreateTriesTheOtherAvailabilityDomainsWhenOneHasNoCapacity(t *testing.T) {
	f, p := newFake(t)
	f.noCapacityIn["Uocm:EU-FRANKFURT-1-AD-1"] = true
	f.noCapacityIn["Uocm:EU-FRANKFURT-1-AD-2"] = true
	got, err := p.Create(context.Background(), spec("burst-a"))
	if err != nil {
		t.Fatal(err)
	}
	if f.lastLaunch["availabilityDomain"] != "Uocm:EU-FRANKFURT-1-AD-3" || got.State != compute.StatePending || f.launches != 3 {
		t.Errorf("launches = %d, last domain %v", f.launches, f.lastLaunch["availabilityDomain"])
	}
	tokens := map[string]bool{}
	for _, h := range f.launchHeaders {
		tokens[h.Get("opc-retry-token")] = true
	}
	if len(tokens) != 3 {
		t.Errorf("the three launches share retry tokens: %v", tokens)
	}
	// Domains are looked up once.
	before := len(f.requests)
	if _, err := p.Create(context.Background(), spec("burst-b")); err != nil {
		t.Fatal(err)
	}
	for _, r := range f.requests[before:] {
		if strings.Contains(r, "availabilityDomains") {
			t.Error("the availability domains were asked for again")
		}
	}
}

func TestNoCapacityAnywhereIsACapacityError(t *testing.T) {
	f, p := newFake(t)
	for _, d := range f.domains {
		f.noCapacityIn[d] = true
	}
	_, err := p.Create(context.Background(), spec("burst-a"))
	var capacity *compute.CapacityError
	if !errors.As(err, &capacity) {
		t.Errorf("err = %v", err)
	}
	if !compute.IsTransient(err) {
		t.Error("a stockout is not transient")
	}
}

func TestProviderFailuresBecomeTheErrorsCallersActOn(t *testing.T) {
	for name, tc := range map[string]struct {
		status        int
		code, message string
		check         func(error) bool
	}{
		"limit":    {400, "LimitExceeded", "The following service limits were exceeded: standard-e4-core-count.", compute.IsQuota},
		"quota":    {400, "QuotaExceeded", "compartment quota", compute.IsQuota},
		"capacity": {500, "InternalError", "Out of host capacity.", func(err error) bool { var c *compute.CapacityError; return errors.As(err, &c) }},
		"auth":     {401, "NotAuthenticated", "not authenticated", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"policy":   {403, "NotAuthorized", "denied", func(err error) bool { return errors.Is(err, compute.ErrAuth) }},
		"rate":     {429, "TooManyRequests", "slow down", func(err error) bool { var r *compute.RateLimitError; return errors.As(err, &r) }},
	} {
		t.Run(name, func(t *testing.T) {
			f, p := newFake(t)
			f.failLaunch = func() (int, map[string]any) {
				return tc.status, map[string]any{"code": tc.code, "message": tc.message}
			}
			if _, err := p.Create(context.Background(), spec("burst-a")); err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
		})
	}
	t.Run("an internal error that is not a stockout stays an error", func(t *testing.T) {
		f, p := newFake(t)
		f.failLaunch = func() (int, map[string]any) { return 500, map[string]any{"code": "InternalError", "message": "boom"} }
		_, err := p.Create(context.Background(), spec("burst-a"))
		if err == nil || compute.IsTransient(err) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestAClockThatIsOffGetsAnAuthErrorThatSaysSo(t *testing.T) {
	f, p := newFake(t)
	p.now = func() time.Time { return time.Now().Add(-time.Hour) }
	_, err := p.Verify(context.Background())
	if !errors.Is(err, compute.ErrAuth) || !strings.Contains(err.Error(), "clock") {
		t.Errorf("err = %v", err)
	}
	if len(f.badRequests) == 0 {
		t.Error("the fake did not notice the skew")
	}
}

func TestGetFindsTheAddressesThroughTheVNICAttachment(t *testing.T) {
	f, p := newFake(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	ocid := created.ID[strings.Index(created.ID, "/")+1:]

	pending, err := p.Get(context.Background(), created.ID)
	if err != nil || pending.PublicIPv4 != "" || pending.Ready() {
		t.Fatalf("a machine without a VNIC yet: %+v, %v", pending, err)
	}
	f.run(ocid, "130.61.0.9")
	got, err := p.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Ready() || got.PublicIPv4 != "130.61.0.9" || got.PrivateIPv4 != "10.0.0.7" || got.CreatedAt.IsZero() {
		t.Errorf("got %+v", got)
	}
	// A bare OCID works too: the region is in it.
	bare, err := p.Get(context.Background(), ocid)
	if err != nil || bare.ID != created.ID {
		t.Errorf("bare OCID: %+v, %v", bare, err)
	}
	f.noBadSignatures()
}

func TestAMachinesStateIsTranslated(t *testing.T) {
	for state, want := range map[string]compute.State{
		"PROVISIONING": compute.StatePending, "STARTING": compute.StatePending, "MOVING": compute.StatePending,
		"RUNNING": compute.StateRunning, "CREATING_IMAGE": compute.StateRunning,
		"STOPPING": compute.StateStopped, "STOPPED": compute.StateStopped,
		"TERMINATING": compute.StateTerminating, "TERMINATED": compute.StateGone,
	} {
		if got := stateOf(state); got != want {
			t.Errorf("%s -> %s, want %s", state, got, want)
		}
	}
}

func TestListFindsOnlyTheOwnersMachinesInEveryPageAndRegion(t *testing.T) {
	f, p := newFake(t)
	f.pageSize = 2
	for _, tc := range []struct{ name, cluster, pool string }{
		{"burst-a", "c1", "burst"}, {"burst-b", "c1", "burst"}, {"burst-c", "c1", "burst"}, {"web-a", "c1", "web"}, {"theirs", "someone-else", "burst"},
	} {
		s := spec(tc.name)
		s.Labels = compute.Owner{Cluster: tc.cluster, Pool: tc.pool}.Labels(tc.name)
		if _, err := p.Create(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	// A stranger's instance, with no tags.
	f.instances["ocid1.instance.oc1.eu-frankfurt-1.stranger"] = map[string]any{"id": "ocid1.instance.oc1.eu-frankfurt-1.stranger", "displayName": "stranger", "lifecycleState": "RUNNING"}
	f.order = append(f.order, "ocid1.instance.oc1.eu-frankfurt-1.stranger")
	for id, inst := range f.instances {
		if inst["displayName"] == "burst-b" {
			f.run(id, "130.61.0.9")
		}
		if inst["displayName"] == "burst-c" {
			f.state(id, "TERMINATED")
		}
	}

	burst, err := p.List(context.Background(), compute.Owner{Cluster: "c1", Pool: "burst", Regions: []string{testRegion, testRegion}})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]compute.Instance{}
	for _, i := range burst {
		byName[i.Name] = i
	}
	if len(burst) != 3 || byName["burst-a"].ID == "" || byName["burst-b"].ID == "" || byName["burst-c"].ID == "" {
		t.Fatalf("listed %+v", burst)
	}
	if byName["burst-b"].PublicIPv4 != "130.61.0.9" || byName["burst-b"].State != compute.StateRunning {
		t.Errorf("burst-b = %+v", byName["burst-b"])
	}
	if byName["burst-c"].State != compute.StateGone || byName["burst-c"].Alive() {
		t.Errorf("a terminated machine is listed as %s", byName["burst-c"].State)
	}
	if byName["burst-a"].Labels[compute.LabelNode] != "burst-a" {
		t.Errorf("labels = %v", byName["burst-a"].Labels)
	}
	all, _ := p.List(context.Background(), compute.Owner{Cluster: "c1"})
	if len(all) != 4 {
		t.Errorf("the cluster's machines: %d, want 4", len(all))
	}
	none, _ := p.List(context.Background(), compute.Owner{Cluster: "nobody"})
	if len(none) != 0 {
		t.Errorf("another owner's machines: %+v", none)
	}
	f.noBadSignatures()
}

func TestDeleteIsIdempotentRemovesTheBootVolumeAndGetReportsAGoneMachine(t *testing.T) {
	f, p := newFake(t)
	created, _ := p.Create(context.Background(), spec("burst-a"))
	ocid := created.ID[strings.Index(created.ID, "/")+1:]
	// The fake refuses a delete that does not say preserveBootVolume=false, as Oracle would leave the volume billing.
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting a machine that is being terminated: %v", err)
	}
	got, err := p.Get(context.Background(), created.ID)
	if err != nil || got.State != compute.StateTerminating || got.Alive() {
		t.Errorf("a terminating machine: %+v, %v", got, err)
	}
	f.state(ocid, "TERMINATED")
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting a terminated machine: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get of a terminated machine = %v, want ErrNotFound", err)
	}
	delete(f.instances, ocid)
	if err := p.Delete(context.Background(), created.ID); err != nil {
		t.Errorf("deleting a machine that is gone: %v", err)
	}
	if _, err := p.Get(context.Background(), created.ID); !errors.Is(err, compute.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
	if err := p.Delete(context.Background(), "garbage"); err != nil {
		t.Errorf("deleting an id that names nothing: %v", err)
	}
	f.noBadSignatures()
}

func TestSizesAreSmallestFirstWithFlexShapesInSteps(t *testing.T) {
	_, p := newFake(t)
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]compute.Size{}
	for _, s := range sizes {
		byID[s.ID] = s
	}
	for _, skipped := range []string{"BM.Standard.E4.128", "VM.GPU.A10.1", "VM.DenseIO.E4.Flex"} {
		if _, ok := byID[skipped]; ok {
			t.Errorf("%s is offered", skipped)
		}
	}
	if e4 := byID["VM.Standard.E4.Flex:2:8"]; e4.VCPUs != 4 || e4.MemoryMiB != 8192 || e4.Arch != "amd64" || e4.HourlyPrice != 0 {
		t.Errorf("an OCPU is two vCPUs on x86: %+v", e4)
	}
	if a1 := byID["VM.Standard.A1.Flex:4:16"]; a1.VCPUs != 4 || a1.Arch != "arm64" {
		t.Errorf("an OCPU is one vCPU on Arm: %+v", a1)
	}
	if fixed := byID["VM.Standard2.2"]; fixed.VCPUs != 4 || fixed.MemoryMiB != 30*1024 {
		t.Errorf("fixed shape = %+v", fixed)
	}
	count := 0
	for _, s := range sizes {
		if s.ID == "VM.Standard2.2" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("a shape listed twice by Oracle is offered %d times", count)
	}
	for i := 1; i < len(sizes); i++ {
		a, b := sizes[i-1], sizes[i]
		if a.VCPUs > b.VCPUs || (a.VCPUs == b.VCPUs && a.MemoryMiB > b.MemoryMiB) {
			t.Fatalf("not smallest first: %+v before %+v", a, b)
		}
	}
	// Every Flex size can be given back to Create.
	for _, s := range sizes {
		if _, _, _, err := parseSize(s.ID); err != nil {
			t.Errorf("Size %s cannot be created: %v", s.ID, err)
		}
	}
}

func TestListsThatNeedSeveralPagesAreReadWhole(t *testing.T) {
	f, p := newFake(t)
	f.pageSize = 3
	sizes, err := p.Sizes(context.Background(), "")
	if err != nil || len(sizes) == 0 {
		t.Fatalf("%v, %d sizes", err, len(sizes))
	}
	// 7 shapes at 3 a page is three pages.
	pages := 0
	for _, r := range f.requests {
		if r == "GET /20160918/shapes" {
			pages++
		}
	}
	if pages != 3 {
		t.Errorf("%d shape pages read, want 3", pages)
	}
	f.noBadSignatures()
}

func TestVerifyIsAHarmlessReadThatWarnsAboutTheSecurityList(t *testing.T) {
	f, p := newFake(t)
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(account.Name, "geass-compartment") || len(account.Warnings) == 0 || !strings.Contains(account.Warnings[0], "security list") {
		t.Errorf("account = %+v", account)
	}
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET") {
			t.Errorf("Verify changed something: %s", r)
		}
	}
	noRegion, _ := New(testCredentials(), compute.Options{BaseURL: p.baseURL})
	if _, err := noRegion.Verify(context.Background()); err == nil {
		t.Error("Verify passed without a region")
	}
}

func TestAUserWhoCannotReadTheCompartmentRecordStillVerifies(t *testing.T) {
	f, p := newFake(t)
	f.noCompartmentAccess = true
	account, err := p.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify = %v: launching does not need the right to inspect compartments", err)
	}
	if !strings.Contains(account.Name, testCompartment) {
		t.Errorf("name = %q, want the OCID when the name cannot be read", account.Name)
	}
}

func TestAUserWhoCannotListAvailabilityDomainsIsToldToNameOne(t *testing.T) {
	f, p := newFake(t)
	f.domainsForbidden = true
	_, err := p.Create(context.Background(), spec("burst-a"))
	if err == nil || !strings.Contains(err.Error(), SettingAvailabilityDomain) {
		t.Fatalf("err = %v", err)
	}
	s := spec("burst-a")
	s.Settings[SettingAvailabilityDomain] = "Uocm:EU-FRANKFURT-1-AD-1"
	if _, err := p.Create(context.Background(), s); err != nil {
		t.Errorf("naming the domain did not avoid the call: %v", err)
	}
}

func TestIDsFindTheirRegion(t *testing.T) {
	p, _ := New(testCredentials(), compute.Options{Region: "eu-frankfurt-1"})
	for id, want := range map[string]string{
		"ap-sydney-1/ocid1.instance.oc1.ap-sydney-1.abc": "ap-sydney-1",
		"phx/ocid1.instance.oc1.phx.abc":                 "us-phoenix-1",
		"ocid1.instance.oc1.iad.abc":                     "us-ashburn-1",
		"ocid1.instance.oc1.uk-london-1.abc":             "uk-london-1",
		"ocid1.instance.oc1..abc":                        "eu-frankfurt-1",
	} {
		region, _, err := p.splitID(id)
		if err != nil || region != want {
			t.Errorf("%s -> %s, %v; want %s", id, region, err, want)
		}
	}
	for _, id := range []string{"", "garbage", "us-phoenix-1/vm-1", "ocid1.subnet.oc1.phx.x"} {
		if _, _, err := p.splitID(id); !errors.Is(err, compute.ErrNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestARegionThatCouldBeAHostInjectionIsRefused(t *testing.T) {
	_, p := newFake(t)
	s := spec("burst-a")
	s.Region = "evil.example.com/x"
	if _, err := p.Create(context.Background(), s); err == nil {
		t.Error("a region that is not a region identifier was used to build an address")
	}
}

func TestTheOSFirewallIsOpenedAheadOfTheRejectRule(t *testing.T) {
	var init compute.CloudInit
	p, _ := New(testCredentials(), compute.Options{})
	var customizer compute.CloudInitCustomizer = p
	customizer.CustomizeCloudInit(&init, spec("burst-a"))
	doc := init.Render()
	for _, want := range []string{"--dport 51820", "--dport 10250", "netfilter-persistent save", "iptables -I INPUT"} {
		if !strings.Contains(doc, want) {
			t.Errorf("the cloud-init document does not contain %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "ufw") {
		t.Error("ufw is used: Canonical warns it can stop an Oracle instance from booting")
	}

	// Run the commands against a stand-in iptables that holds Oracle's Ubuntu INPUT chain (REJECT is rule 6).
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run the commands with")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := `#!/bin/sh
echo "iptables $*" >> "$FAKE_DIR/log"
case "$1" in
-L)
  echo "Chain INPUT (policy ACCEPT)"
  echo "num  target     prot opt source               destination"
  echo "1    ACCEPT     all  --  0.0.0.0/0            0.0.0.0/0            state RELATED,ESTABLISHED"
  echo "2    ACCEPT     icmp --  0.0.0.0/0            0.0.0.0/0"
  echo "3    ACCEPT     all  --  0.0.0.0/0            0.0.0.0/0"
  echo "4    ACCEPT     udp  --  0.0.0.0/0            0.0.0.0/0            udp spt:123"
  echo "5    ACCEPT     tcp  --  0.0.0.0/0            0.0.0.0/0            state NEW tcp dpt:22"
  echo "6    REJECT     all  --  0.0.0.0/0            0.0.0.0/0            reject-with icmp-host-prohibited"
  ;;
-C)
  shift; shift
  grep -qx -- "$*" "$FAKE_DIR/added" 2>/dev/null
  ;;
-I)
  shift; shift; shift
  echo "$*" >> "$FAKE_DIR/added"
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "iptables"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "netfilter-persistent"), []byte("#!/bin/sh\necho \"netfilter-persistent $*\" >> \"$FAKE_DIR/log\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() {
		for _, command := range init.RunCommands {
			cmd := exec.Command("sh", "-c", command)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_DIR="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%q failed: %v\n%s", command, err, out)
			}
		}
	}
	run()
	run() // the commands are safe to run twice
	raw, _ := os.ReadFile(log)
	var inserts, saves []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		switch {
		case strings.HasPrefix(line, "iptables -I"):
			inserts = append(inserts, line)
		case strings.HasPrefix(line, "netfilter-persistent"):
			saves = append(saves, line)
		}
	}
	slices.Sort(inserts)
	want := []string{"iptables -I INPUT 6 -p tcp --dport 10250 -j ACCEPT", "iptables -I INPUT 6 -p udp --dport 51820 -j ACCEPT"}
	if strings.Join(inserts, "|") != strings.Join(want, "|") {
		t.Errorf("inserts = %v\nwant %v (in front of the REJECT, once each, even though the commands ran twice)", inserts, want)
	}
	if len(saves) != 2 || saves[0] != "netfilter-persistent save" {
		t.Errorf("saves = %v", saves)
	}
}

func TestTheProviderRegistersItself(t *testing.T) {
	d, ok := compute.Lookup(ID)
	if !ok || d.ID != "OCI" {
		t.Fatalf("descriptor = %+v", d)
	}
	keys := map[string]compute.Field{}
	for _, f := range d.Fields {
		keys[f.Key] = f
	}
	for _, key := range []string{"tenancyOcid", "userOcid", "fingerprint", "privateKey", "compartmentOcid"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("field %s is missing", key)
		}
	}
	if !keys["privateKey"].Secret || !keys["privateKey"].Multiline {
		t.Error("the private key must be a secret, multiline field")
	}
	for _, key := range []string{SettingAvailabilityDomain, SettingSubnetID, SettingImageID, SettingNSGID, SettingDiskSizeGB} {
		if !d.KnownSetting(key) {
			t.Errorf("setting %s is not described", key)
		}
	}
	if _, err := compute.New(ID, compute.Credentials{}, compute.Options{}); err == nil {
		t.Error("a provider was built without credentials")
	}
	built, err := compute.New(ID, testCredentials(), compute.Options{Region: testRegion})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := built.(compute.CloudInitCustomizer); !ok {
		t.Error("the provider does not customize cloud-init, though Oracle's Ubuntu image needs it")
	}
}
