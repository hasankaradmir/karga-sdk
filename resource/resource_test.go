package resource

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const workloadDoc = `
apiVersion: karga.io/v1alpha1
kind: Workload
metadata:
  name: redis
spec:
  image: redis:7.4
  environment:
    REDIS_MAXMEMORY: 256mb
  ports:
    - name: redis
      containerPort: 6379
  mounts:
    - volume: redis-data
      mountPath: /data
  healthCheck:
    type: exec
    command: [redis-cli, ping]
    interval: 10s
    timeout: 3s
    retries: 3
  resources:
    memory: 512Mi
    cpu: 1
`

func TestDecodeWorkload(t *testing.T) {
	r, err := Decode([]byte(workloadDoc))
	if err != nil {
		t.Fatal(err)
	}
	w, ok := r.Spec.(*WorkloadSpec)
	if !ok {
		t.Fatalf("spec type %T", r.Spec)
	}
	if r.Kind != KindWorkload || r.Metadata.Name != "redis" || w.Image != "redis:7.4" {
		t.Fatalf("unexpected: %+v %+v", r, w)
	}
	if w.RestartPolicy != "always" || w.PullPolicy != "if-not-present" || w.Ports[0].Protocol != "tcp" {
		t.Fatalf("defaults not applied: %+v", w)
	}
	if w.Resources.CPU != "1" || w.Resources.Memory != "512Mi" || w.Resources.Pids == nil || *w.Resources.Pids != DefaultPids ||
		*w.Logging != (Logging{MaxSize: "10m", MaxFiles: 3}) || !*w.Security.NoNewPrivileges {
		t.Fatalf("resources: %+v", w.Resources)
	}
}

func TestDecodeDefaults(t *testing.T) {
	v, err := Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Volume\nmetadata: {name: d}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Spec, &VolumeSpec{Driver: "local", Retention: "retain"}) {
		t.Fatalf("volume defaults: %+v", v.Spec)
	}
	n, err := Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n.Spec.(*NetworkSpec).Driver != "bridge" {
		t.Fatalf("network defaults: %+v", n.Spec)
	}
	sv, err := Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Service\nmetadata: {name: s}\nspec: {workload: w, ports: [{name: http, port: 80}], expose: [{port: http, listener: web, host: app.local}]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sv.Spec.(*ServiceSpec); got.Ports[0].TargetPort != "http" || got.Expose[0].Path != "/" {
		t.Fatalf("service defaults: %+v", got)
	}
	hw, err := Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a, ports: [{name: dns, containerPort: 53, protocol: udp}, {name: http, containerPort: 8080}], healthCheck: {type: http}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if hc := hw.Spec.(*WorkloadSpec).HealthCheck; hc.Port != 8080 || hc.Path != "/" {
		t.Fatalf("http healthcheck defaults: %+v", hc)
	}
}

func TestDecodeMovedFields(t *testing.T) {
	for _, f := range []string{"hostPort: 80", "hostIP: 0.0.0.0", "routes: [{host: a}]"} {
		_, err := Decode([]byte("apiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a, ports: [{name: http, containerPort: 80, " + f + "}]}\n"))
		if err == nil || !strings.Contains(err.Error(), "a Service does") {
			t.Errorf("%s: %v", f, err)
		}
	}
	// What karga stored before still reads.
	r, err := DecodeStored([]byte(`{"apiVersion":"karga.io/v1alpha1","kind":"Workload","metadata":{"name":"w"},"spec":{"image":"a","ports":[{"containerPort":80,"hostPort":80,"hostIP":"127.0.0.1"}]}}`))
	if err != nil || r.Spec.(*WorkloadSpec).Ports[0].ContainerPort != 80 {
		t.Fatalf("stored: %+v %v", r, err)
	}
}

func TestDecodeEmpty(t *testing.T) {
	for _, doc := range []string{"", "  \n", "# only a comment\n"} {
		if _, err := Decode([]byte(doc)); !errors.Is(err, ErrEmpty) {
			t.Errorf("Decode(%q): want ErrEmpty, got %v", doc, err)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	head := "apiVersion: karga.io/v1alpha1\nmetadata: {name: x}\n"
	tests := []struct{ doc, want string }{
		{"apiVersion: karga.io/v1\nkind: Volume\nmetadata: {name: x}\n", "apiVersion"},
		{head + "kind: Deployment\n", `unknown kind "Deployment"`},
		{head + "kind: Volume\nspec: {driverr: local}\n", "driverr"},
		{head + "kind: Volume\nextra: 1\n", "extra"},
		{head + "kind: Volume\nspec: {retention: forever}\n", "retention"},
		{head + "kind: Workload\nspec: {}\n", "spec.image is required"},
		{head + "kind: Workload\nspec: {image: a, restartPolicy: no}\n", "restartPolicy"},
		{head + "kind: Workload\nspec: {image: a, ports: [{containerPort: 0}]}\n", "containerPort"},
		{head + "kind: Workload\nspec: {image: a, ports: [{containerPort: 1, protocol: sctp}]}\n", "protocol"},
		{head + "kind: Workload\nspec: {image: a, mounts: [{mountPath: /d}]}\n", "exactly one"},
		{head + "kind: Workload\nspec: {image: a, pullPolicy: sometimes}\n", "pullPolicy"},
		{head + "kind: Workload\nspec: {image: a, updateStrategy: {type: rolling}}\n", "updateStrategy.type"},
		{head + "kind: Workload\nspec: {image: a, updateStrategy: {drainTimeout: 0s}}\n", "drainTimeout"},
		{head + "kind: Workload\nspec: {image: a, stopTimeout: 500ms}\n", "stopTimeout"},
		{head + "kind: Workload\nspec: {image: a, stopTimeout: 2h}\n", "stopTimeout"},
		{head + "kind: Workload\nspec: {image: a, updateStrategy: {type: replace, proxy: true}}\n", "proxy"},
		{head + "kind: Workload\nspec: {image: a, ports: [{containerPort: 1}]}\n", "ports[0].name"},
		{head + "kind: Workload\nspec: {image: a, ports: [{name: a, containerPort: 1}, {name: a, containerPort: 2}]}\n", "used twice"},
		{head + "kind: Service\nspec: {workload: w}\n", "at least one port"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 70000}]}\n", "port: 70000 out of range"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}, {name: b, port: 1}]}\n", "1 is used twice"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: b, listener: web}]}\n", "not a port of the service"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: a, listener: web, host: A_b}]}\n", "host name"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: a, listener: web, host: a, path: api}]}\n", "must start with /"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: a, listener: pg, tls: {secret: c}}]}\n", "need a host"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: a, listener: web, host: a, tls: {secret: Bad_Name}}]}\n", "must name a Secret"},
		{head + "kind: Service\nspec: {workload: w, ports: [{name: a, port: 1}], expose: [{port: a, listener: web, host: a}, {port: a, listener: web, host: a}]}\n", "already serves"},
		{head + "kind: Workload\nspec: {image: a, mounts: [{volume: v, config: c, mountPath: /d}]}\n", "exactly one"},
		{head + "kind: Workload\nspec: {image: a, mounts: [{volume: v, mountPath: data}]}\n", "absolute"},
		{head + "kind: Workload\nspec: {image: a, healthCheck: {type: grpc}}\n", "healthCheck.type"},
		{head + "kind: Workload\nspec: {image: a, healthCheck: {type: tcp}}\n", "healthCheck.port"},
		{head + "kind: Workload\nspec: {image: a, ports: [{name: http, containerPort: 80}], healthCheck: {type: http, port: 81}}\n", "healthCheck.port"},
		{head + "kind: Workload\nspec: {image: a, ports: [{name: http, containerPort: 80}], healthCheck: {type: tcp, command: [x]}}\n", "not used by type tcp"},
		{head + "kind: Workload\nspec: {image: a, ports: [{name: http, containerPort: 80}], healthCheck: {type: http, path: health}}\n", "must start with /"},
		{head + "kind: Workload\nspec: {image: a, healthCheck: {type: exec}}\n", "healthCheck.command"},
		{head + "kind: Workload\nspec: {image: a, healthCheck: {type: exec, command: [x], interval: 10}}\n", "interval"},
		{head + "kind: Workload\nspec: {image: a, healthCheck: {type: exec, command: [x], startPeriod: soon}}\n", "startPeriod"},
		{head + "kind: Workload\nspec: {image: a, resources: {memory: 512MB}}\n", "memory"},
		{head + "kind: Workload\nspec: {image: a, resources: {cpu: -1}}\n", "cpu"},
		{head + "kind: Workload\nspec: {image: a, resources: {pids: -1}}\n", "resources.pids"},
		{head + "kind: Workload\nspec: {image: a, logging: {maxSize: 10MB}}\n", "logging.maxSize"},
		{head + "kind: Workload\nspec: {image: a, security: {capAdd: [net-admin]}}\n", "not a capability name"},
		{head + "kind: Workload\nspec: {image: a, security: {user: \"1000 1000\"}}\n", "security.user"},
		{head + "kind: Workload\nspec: {image: a, logging: {maxFiles: -1}}\n", "logging.maxFiles"},
		{head + "kind: Workload\nspec: {image: a, environment: {PORT: 6379}}\n", "environment"},
		{head + "kind: Config\nspec: {files: {}}\n", "must not be empty"},
		{head + "kind: Config\nspec: {files: {../x: a}}\n", "not a valid file name"},
		{head + "kind: Secret\nspec: {}\n", "spec.values must not be empty"},
		{head + "kind: Secret\nspec: {values: {a/b: x}}\n", "not a valid file name"},
	}
	for _, tt := range tests {
		_, err := Decode([]byte(tt.doc))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("want error containing %q, got %v\n%s", tt.want, err, tt.doc)
		}
	}
}

func mustDecode(t *testing.T, doc string) Resource {
	t.Helper()
	r, err := Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestValidateSet(t *testing.T) {
	vol := mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Volume\nmetadata: {name: redis-data}\n")
	net := mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: n}\n")
	w := mustDecode(t, workloadDoc)
	if err := ValidateSet([]Resource{vol, w}); err != nil {
		t.Fatalf("valid set: %v", err)
	}

	tests := []struct {
		name string
		rs   []Resource
		want string
	}{
		{"missing volume", []Resource{w}, `Volume "redis-data" is not defined`},
		{"missing network", []Resource{vol, withNetwork(w, "backend")}, `network "backend" is not defined`},
		{"duplicate", []Resource{vol, vol, w}, "defined more than once"},
		{"bad name", []Resource{mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: Bad_Name}\n")}, "DNS label"},
		{"reserved name", []Resource{mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: karga-net}\n")}, "reserved for karga"},
		{"service without workload", []Resource{vol, w, service(t, "s", "nope", 80, "redis", "")}, `workload "nope" is not defined`},
		{"service off network", []Resource{vol, w, service(t, "s", "redis", 80, "redis", "")}, "joins no network"},
		{"unknown target port", []Resource{vol, net, withNetwork(w, "n"), service(t, "s", "redis", 80, "web", "")}, `targetPort "web" is not a port`},
		{"target port by number", nil, ""},
		{"service port clash", []Resource{vol, net, withNetwork(w, "n"), service(t, "a", "redis", 80, "redis", ""), service(t, "b", "redis", 80, "redis", "")}, `port 80 is already used by Service "a"`},
		{"exposure clash", []Resource{vol, net, withNetwork(w, "n"), service(t, "a", "redis", 80, "redis", "pg"), service(t, "b", "redis", 81, "redis", "pg")}, `listener pg  is already used by Service "a"`},
		{"missing tls secret", []Resource{vol, net, withNetwork(w, "n"), withTLS(t, "cert")}, `tls secret "cert" is not defined`},
		{"tls secret without key", []Resource{vol, net, withNetwork(w, "n"), withTLS(t, "cert"),
			mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Secret\nmetadata: {name: cert}\nspec: {values: {tls.crt: x}}\n"),
		}, "needs tls.crt and tls.key"},
		{"config is not a volume", []Resource{
			mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Config\nmetadata: {name: redis-data}\nspec: {files: {a: b}}\n"), w,
		}, `Volume "redis-data" is not defined`},
	}
	if err := ValidateSet([]Resource{vol, net, withNetwork(w, "n"), service(t, "s", "redis", 80, "6379", "")}); err != nil {
		t.Fatalf("target port by number: %v", err)
	}
	for _, tt := range tests {
		if tt.rs == nil {
			continue
		}
		err := ValidateSet(tt.rs)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: want error containing %q, got %v", tt.name, tt.want, err)
		}
	}
}

// service is a Service for workload with one port; listener exposes it as tcp.
func service(t *testing.T, name, workload string, port int, target, listener string) Resource {
	t.Helper()
	doc := fmt.Sprintf("apiVersion: karga.io/v1alpha1\nkind: Service\nmetadata: {name: %s}\nspec: {workload: %s, ports: [{name: p, port: %d, targetPort: %q}]", name, workload, port, target)
	if listener != "" {
		doc += fmt.Sprintf(", expose: [{port: p, listener: %s}]", listener)
	}
	return mustDecode(t, doc+"}\n")
}

func withTLS(t *testing.T, secret string) Resource {
	t.Helper()
	return mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Service\nmetadata: {name: s}\nspec: {workload: redis, ports: [{name: p, port: 443, targetPort: redis}], expose: [{port: p, listener: websecure, host: a.example, tls: {secret: "+secret+"}}]}\n")
}

func withNetwork(r Resource, n string) Resource {
	w := *r.Spec.(*WorkloadSpec)
	w.Networks = []string{n}
	r.Spec = &w
	return r
}

func TestParseQuantities(t *testing.T) {
	mem := map[string]int64{"512Mi": 512 << 20, "1Gi": 1 << 30, "1G": 1e9, "1048576": 1048576, "1.5Ki": 1536}
	for in, want := range mem {
		if got, err := ParseMemory(in); err != nil || got != want {
			t.Errorf("ParseMemory(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	cpu := map[string]int64{"1": 1000, "0.5": 500, "500m": 500, "2": 2000}
	for in, want := range cpu {
		if got, err := ParseCPU(in); err != nil || got != want {
			t.Errorf("ParseCPU(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1", "0", "512MB", "NaN", "Inf", "1e400"} {
		if _, err := ParseMemory(bad); err == nil {
			t.Errorf("ParseMemory(%q): expected error", bad)
		}
	}
	for _, bad := range []string{"", "x", "0", "0.0001", "NaN", "-1m"} {
		if _, err := ParseCPU(bad); err == nil {
			t.Errorf("ParseCPU(%q): expected error", bad)
		}
	}
}

func TestDecodeYAML12(t *testing.T) {
	// YAML 1.1 would turn these into booleans.
	r := mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Network\nmetadata: {name: n}\n")
	if r.Metadata.Name != "n" {
		t.Fatalf("name: %q", r.Metadata.Name)
	}
	w := mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a, environment: {FLAG: no}}\n")
	if w.Spec.(*WorkloadSpec).Environment["FLAG"] != "no" {
		t.Fatalf("env: %+v", w.Spec)
	}
}

func TestUpdateStrategyDefaults(t *testing.T) {
	w := mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a}\n").Spec.(*WorkloadSpec)
	if typ, drain := w.Strategy(); typ != StrategyRecreate || drain != DefaultDrainTimeout || w.UpdateStrategy != nil {
		t.Fatalf("defaults: %s %s %+v", typ, drain, w.UpdateStrategy)
	}
	w = mustDecode(t, "apiVersion: karga.io/v1alpha1\nkind: Workload\nmetadata: {name: w}\nspec: {image: a, updateStrategy: {type: replace, drainTimeout: 30s}, mounts: [{volume: v, mountPath: /v, readOnly: true}]}\n").Spec.(*WorkloadSpec)
	if typ, drain := w.Strategy(); typ != StrategyReplace || drain != 30*time.Second || !w.Mounts[0].ReadOnly {
		t.Fatalf("explicit: %s %s %+v", typ, drain, w.Mounts)
	}
}
