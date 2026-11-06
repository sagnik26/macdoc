package tests

import (
	"context"
	"errors"
	"github.com/sagnik26/macdoc/internal/probe"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stubPurgeable replaces the shim for one test.
func stubPurgeable(t *testing.T, n int64, ok bool) {
	t.Helper()
	old := probe.PurgeableLookup
	probe.PurgeableLookup = func() (int64, bool) { return n, ok }
	t.Cleanup(func() { probe.PurgeableLookup = old })
}

func TestParseDiskutilInfoRealFixture(t *testing.T) {
	ref, total, free, err := probe.ParseDiskutilInfo(readFixture(t, "diskutil_info_root.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if ref != "disk3" || total != 245107195904 || free != 58778763264 {
		t.Errorf("got ref=%q total=%d free=%d", ref, total, free)
	}
}

func TestParseDiskutilInfoFallback(t *testing.T) {
	ref, total, free, err := probe.ParseDiskutilInfo(readFixture(t, "w2_diskutil_info_nofree.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if ref != "disk5" || total != 987654321 || free != 123456789 {
		t.Errorf("got ref=%q total=%d free=%d", ref, total, free)
	}
}

func TestParseDiskutilInfoNoSizes(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict><key>APFSContainerReference</key><string>disk6</string></dict></plist>`)
	ref, total, free, err := probe.ParseDiskutilInfo(data)
	if err != nil || ref != "disk6" || total != 0 || free != 0 {
		t.Errorf("got ref=%q total=%d free=%d err=%v", ref, total, free, err)
	}
}

func TestParseDiskutilInfoErrors(t *testing.T) {
	for _, tc := range []struct{ fixture, want string }{
		{"w2_diskutil_info_hfs.plist", "no APFS container reference"},
		{"w2_malformed.plist", "parse diskutil info plist"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			_, _, _, err := probe.ParseDiskutilInfo(readFixture(t, tc.fixture))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseAPFSListRealFixture(t *testing.T) {
	vols, err := probe.ParseAPFSList(readFixture(t, "diskutil_apfs_list.plist"), "disk3")
	if err != nil {
		t.Fatal(err)
	}
	want := []probe.Volume{
		{Name: "Data", Roles: []string{"Data"}, UsedBytes: 157538500608},
		{Name: "Macintosh HD", Roles: []string{"System"}, UsedBytes: 12638056448},
		{Name: "Preboot", Roles: []string{"Preboot"}, UsedBytes: 9337528320},
		{Name: "VM", Roles: []string{"VM"}, UsedBytes: 5368926208},
		{Name: "Recovery", Roles: []string{"Recovery"}, UsedBytes: 1299750912},
		{Name: "Update", Roles: []string{"Update"}, UsedBytes: 8417280},
	}
	if !reflect.DeepEqual(vols, want) {
		t.Errorf("volumes:\n got %+v\nwant %+v", vols, want)
	}
}

func TestParseAPFSListMountPointAndMissingRoles(t *testing.T) {
	vols, err := probe.ParseAPFSList(readFixture(t, "w2_apfs_list_mount.plist"), "disk7")
	if err != nil {
		t.Fatal(err)
	}
	want := []probe.Volume{
		{Name: "Big", Roles: []string{"Data"}, MountPoint: "/Volumes/Big", UsedBytes: 5000},
		{Name: "Small", Roles: []string{}, UsedBytes: 100},
	}
	if !reflect.DeepEqual(vols, want) {
		t.Errorf("volumes:\n got %+v\nwant %+v", vols, want)
	}
}

func TestParseAPFSListErrors(t *testing.T) {
	if _, err := probe.ParseAPFSList(readFixture(t, "diskutil_apfs_list.plist"), "disk9"); err == nil || !strings.Contains(err.Error(), `"disk9" not found`) {
		t.Errorf("unknown container: err = %v", err)
	}
	if _, err := probe.ParseAPFSList(readFixture(t, "w2_malformed.plist"), "disk3"); err == nil || !strings.Contains(err.Error(), "parse apfs list plist") {
		t.Errorf("malformed: err = %v", err)
	}
}

func diskRunner(t *testing.T) *platform.FakeRunner {
	return &platform.FakeRunner{Responses: map[string]platform.Result{
		"diskutil info -plist /":    {Stdout: readFixture(t, "diskutil_info_root.plist")},
		"diskutil apfs list -plist": {Stdout: readFixture(t, "diskutil_apfs_list.plist")},
	}}
}

func TestCollectDiskSuccess(t *testing.T) {
	stubPurgeable(t, 4096, true)
	r := diskRunner(t)
	d, err := probe.CollectDisk(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if d.ContainerRef != "disk3" || d.ContainerTotalBytes != 245107195904 || d.ContainerFreeBytes != 58778763264 {
		t.Errorf("container: %+v", d)
	}
	if d.PurgeableBytes != (model.Bytes{Value: 4096, Source: model.SourceMeasured}) {
		t.Errorf("purgeable = %+v", d.PurgeableBytes)
	}
	if len(d.Volumes) != 6 || d.Volumes[0].Name != "Data" {
		t.Errorf("volumes = %+v", d.Volumes)
	}
	want := []platform.Command{
		{Name: "diskutil", Args: []string{"info", "-plist", "/"}, Timeout: probe.DiskutilTimeout},
		{Name: "diskutil", Args: []string{"apfs", "list", "-plist"}, Timeout: probe.DiskutilTimeout},
	}
	if !reflect.DeepEqual(r.Calls, want) {
		t.Errorf("calls = %+v", r.Calls)
	}
}

func TestCollectDiskPurgeableUnknown(t *testing.T) {
	stubPurgeable(t, 0, false)
	d, err := probe.CollectDisk(context.Background(), diskRunner(t))
	if err != nil {
		t.Fatal(err)
	}
	want := model.Bytes{Source: model.SourceUnknown, Reason: "purgeable space needs the cgo build"}
	if d.PurgeableBytes != want {
		t.Errorf("purgeable = %+v", d.PurgeableBytes)
	}
}

func TestCollectDiskAPFSListFailurePartial(t *testing.T) {
	stubPurgeable(t, 0, false)
	cases := map[string]func(r *platform.FakeRunner){
		"run error": func(r *platform.FakeRunner) {
			r.Errors = map[string]error{"diskutil apfs list -plist": errors.New("boom")}
		},
		"non-zero exit": func(r *platform.FakeRunner) {
			r.Responses["diskutil apfs list -plist"] = platform.Result{ExitCode: 1, Stderr: []byte("  apfs error  \n")}
		},
		"unknown container": func(r *platform.FakeRunner) {
			r.Responses["diskutil apfs list -plist"] = platform.Result{Stdout: readFixture(t, "w2_apfs_list_mount.plist")}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := diskRunner(t)
			mutate(r)
			d, err := probe.CollectDisk(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "disk volumes unavailable") {
				t.Fatalf("err = %v", err)
			}
			if d.ContainerRef != "disk3" || d.ContainerTotalBytes != 245107195904 || d.ContainerFreeBytes != 58778763264 {
				t.Errorf("partial disk lost container sizes: %+v", d)
			}
			if d.PurgeableBytes.Source != model.SourceUnknown || d.Volumes != nil {
				t.Errorf("partial disk: %+v", d)
			}
			if name == "non-zero exit" && !strings.HasSuffix(err.Error(), "exit status 1: apfs error") {
				t.Errorf("stderr not trimmed into error: %v", err)
			}
		})
	}
}

func TestCollectDiskInfoFailure(t *testing.T) {
	cases := map[string]platform.Result{
		"non-zero exit no stderr": {ExitCode: 1},
		"non-APFS":                {Stdout: readFixture(t, "w2_diskutil_info_hfs.plist")},
	}
	for name, res := range cases {
		t.Run(name, func(t *testing.T) {
			r := &platform.FakeRunner{Responses: map[string]platform.Result{"diskutil info -plist /": res}}
			d, err := probe.CollectDisk(context.Background(), r)
			if err == nil || !reflect.DeepEqual(d, probe.Disk{}) {
				t.Fatalf("d=%+v err=%v", d, err)
			}
			if len(r.Calls) != 1 {
				t.Errorf("apfs list should not run after info failure: %+v", r.Calls)
			}
		})
	}
	r := &platform.FakeRunner{Errors: map[string]error{"diskutil info -plist /": context.DeadlineExceeded}}
	if _, err := probe.CollectDisk(context.Background(), r); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("run error not wrapped: %v", err)
	}
}
