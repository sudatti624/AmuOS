package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/metrics"
)

func TestParsersReadProcFormats(t *testing.T) {
	stat := "cpu  100 0 50 800 50 0 0 0 0 0\ncpu0 1 2 3 4\nbtime 1700000000\n"
	times, boot, err := parseStat(stat)
	if err != nil || times.busy != 150 || times.total != 1000 || boot == nil || *boot != 1700000000 {
		t.Fatalf("stat: %+v %v %v", times, boot, err)
	}
	if v := cpuPercent(cpuTimes{100, 1000}, cpuTimes{150, 1100}); v == nil || *v != 50 {
		t.Fatalf("cpu percent: %v", v)
	}
	if cpuPercent(cpuTimes{150, 1100}, cpuTimes{10, 50}) != nil {
		t.Fatal("a counter reset gave a value")
	}
	mem := parseMeminfo("MemTotal:  1000 kB\nMemAvailable: 250 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	if v := usedPercent(mem["MemTotal"], mem["MemAvailable"], true); v == nil || *v != 75 {
		t.Fatalf("memory: %v", v)
	}
	if usedPercent(mem["SwapTotal"], mem["SwapFree"], true) != nil {
		t.Fatal("a host without swap reported 0% swap use")
	}
	if l1, l5, l15 := parseLoadavg("0.50 1.25 2.00 1/100 1234\n"); *l1 != 0.5 || *l5 != 1.25 || *l15 != 2 {
		t.Fatal("loadavg")
	}
	diskstats := "   8       0 sda 10 0 100 0 5 0 40 0 0 0 0\n   8       1 sda1 10 0 100 0 5 0 40 0 0 0 0\n   7       0 loop0 1 0 8 0 0 0 0 0 0 0 0\n"
	read, written, ok := parseDiskstats(diskstats, func(name string) bool { return name == "sda" })
	if !ok || read != 100*512 || written != 40*512 {
		t.Fatalf("diskstats: %d %d", read, written)
	}
	netdev := "Inter-|   Receive\n face |bytes packets errs drop fifo frame compressed multicast|bytes\n    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0\n  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n"
	rx, tx, ok := parseNetDev(netdev, func(name string) bool { return name != "lo" })
	if !ok || rx != 1000 || tx != 2000 {
		t.Fatalf("net/dev: %d %d", rx, tx)
	}
	mounts := parseMounts("/dev/sda1 / ext4 rw 0 0\nproc /proc proc rw 0 0\n/dev/sda1 /bind ext4 rw 0 0\n/dev/sdb1 /mnt/my\\040data xfs rw 0 0\n/dev/loop0 /snap/x squashfs ro 0 0\ntank /tank zfs rw 0 0\n")
	if len(mounts) != 3 || mounts[1].path != "/mnt/my data" || mounts[2].fstype != "zfs" {
		t.Fatalf("mounts: %+v", mounts)
	}
	if got := parseOSRelease("NAME=\"Debian GNU/Linux\"\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n"); got != "Debian GNU/Linux 13 (trixie)" {
		t.Fatalf("os-release: %q", got)
	}
	if model, count := parseCPUInfo("processor\t: 0\nmodel name\t: Test CPU\nprocessor\t: 1\nmodel name\t: Test CPU\n"); model != "Test CPU" || count != 2 {
		t.Fatalf("cpuinfo: %q %d", model, count)
	}
	if clean("a\x00b\nc") != "abc" {
		t.Fatal("control characters kept")
	}
}

func TestCountersReportNilAfterResets(t *testing.T) {
	var c counters
	start := time.Unix(1000, 0)
	if r := c.rates(start, map[string]uint64{"rx": 100}); r["rx"] != nil {
		t.Fatal("first reading gave a rate")
	}
	if r := c.rates(start.Add(10*time.Second), map[string]uint64{"rx": 600}); r["rx"] == nil || *r["rx"] != 50 {
		t.Fatalf("rate: %v", r["rx"])
	}
	if r := c.rates(start.Add(20*time.Second), map[string]uint64{"rx": 10}); r["rx"] != nil {
		t.Fatal("a counter that went backwards gave a rate")
	}
	if r := c.rates(start.Add(30*time.Second), map[string]uint64{}); len(r) != 0 {
		t.Fatal("a missing counter gave a rate")
	}
}

type fakeCollector struct{ cpu float64 }

func (f *fakeCollector) Sample(now time.Time) (metrics.Sample, error) {
	f.cpu++
	v := f.cpu
	return metrics.Sample{CollectedAt: now.Unix(), CPU: &v}, nil
}

func (f *fakeCollector) HostInfo(now time.Time) (metrics.HostInfo, error) {
	return metrics.HostInfo{CollectedAt: now.Unix(), Hostname: "managed-01", Kernel: "6.12.0", Filesystems: []metrics.Filesystem{}, Interfaces: []metrics.Interface{}}, nil
}

func TestSamplesSurviveOutagesWithinTheBufferLimits(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	a.cfg.Collector = &fakeCollector{}
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	a.clock = func() time.Time { return now }
	a.collect(now)
	if err := a.sync(ctx); err != nil {
		t.Fatal(err)
	}
	// The Control Plane is unreachable for three collections.
	online := a.cfg.AgentServer
	a.cfg.AgentServer = "https://127.0.0.1:1"
	for i := 1; i <= 3; i++ {
		a.collect(now.Add(time.Duration(i) * 15 * time.Second))
	}
	if err := a.sync(ctx); err == nil {
		t.Fatal("sync succeeded while offline")
	}
	a.cfg.AgentServer = online
	if err := a.sync(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.pending) != 0 || a.pendingSize != 0 {
		t.Fatalf("%d samples still pending", len(a.pending))
	}
	detail, err := cp.db.HostDetail(ctx, cp.owner.Token, host.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if detail.Info == nil || detail.Info.Hostname != "managed-01" || detail.Latest == nil || *detail.Latest.CPU != 4 || !detail.Latest.Current {
		t.Fatalf("detail: %+v %+v", detail.Info, detail.Latest)
	}
	if caps := detail.Agent.Capabilities; len(caps) != 4 || caps[1] != "metrics" {
		t.Fatalf("capabilities: %v", caps)
	}
	history, err := cp.db.MetricHistory(ctx, cp.owner.Token, host.ID, "1h", time.Now().Add(time.Minute))
	if err != nil || len(history.Times) != 4 {
		t.Fatalf("history after the outage: %+v %v", history.Times, err)
	}

	// Beyond 10 minutes offline the oldest samples are dropped, never sent.
	a.cfg.AgentServer = "https://127.0.0.1:1"
	for i := range 50 {
		a.collect(now.Add(time.Duration(i) * 15 * time.Second))
	}
	if a.dropped == 0 || a.pending[0].sample.CollectedAt < now.Add(49*15*time.Second-maxBufferAge).Unix() {
		t.Fatalf("buffer kept samples older than the limit: dropped %d", a.dropped)
	}
	if len(a.pending) > int(maxBufferAge/(15*time.Second))+1 {
		t.Fatalf("%d samples pending", len(a.pending))
	}
}

func TestControlPlaneRefusesMalformedSampleBatches(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	_, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	certificate, _, _, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	client := a.client(&certificate)
	now := time.Now().Unix()
	one := []metrics.Sample{{Seq: 1, CollectedAt: now}}
	many := make([]metrics.Sample, metrics.MaxBatch+1)
	for i := range many {
		many[i] = metrics.Sample{Seq: int64(i + 1), CollectedAt: now}
	}
	for name, body := range map[string]any{
		"short run id":  map[string]any{"run_id": "short", "samples": one},
		"bad run id":    map[string]any{"run_id": "run id with spaces!!", "samples": one},
		"empty batch":   map[string]any{"run_id": a.runID, "samples": []metrics.Sample{}},
		"too many":      map[string]any{"run_id": a.runID, "samples": many},
		"unknown field": map[string]any{"run_id": a.runID, "samples": one, "host_id": "other"},
	} {
		if err := post(ctx, client, a.cfg.AgentServer+"/agent/v1/samples", body, nil); !invalid(err) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := post(ctx, client, a.cfg.AgentServer+"/agent/v1/samples", map[string]any{"run_id": a.runID, "samples": one}, nil); err != nil {
		t.Fatal(err)
	}
}
