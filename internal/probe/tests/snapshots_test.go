package tests

import (
	"bufio"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/probe"

	"github.com/sagnik26/macdoc/internal/platform"
)

func TestParseLocalSnapshotsRealFixtureEmpty(t *testing.T) {
	s, err := probe.ParseLocalSnapshots(readFixture(t, "tmutil_listlocalsnapshots.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 0 || len(s.Names) != 0 {
		t.Errorf("got %+v", s)
	}
}

func TestParseLocalSnapshotsSynthetic(t *testing.T) {
	s, err := probe.ParseLocalSnapshots(readFixture(t, "w2_tmutil_snapshots.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := probe.Snapshots{Count: 4, Names: []string{
		"com.apple.TimeMachine.2026-10-25-091500.local",
		"com.apple.TimeMachine.2026-10-26-101500.local",
		"com.apple.TimeMachine.2026-10-27-111500.local",
		"com.bombich.ccc.2026-10-27-120000",
	}}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("got %+v, want %+v", s, want)
	}
}

func TestParseLocalSnapshotsNone(t *testing.T) {
	for _, in := range []string{"", "\n\n", "Snapshots for disk /:\nNo local snapshots\n", "There are no snapshots on disk /\n"} {
		s, err := probe.ParseLocalSnapshots([]byte(in))
		if err != nil || s.Count != 0 || len(s.Names) != 0 {
			t.Errorf("%q: got %+v, %v", in, s, err)
		}
	}
}

func TestParseLocalSnapshotsLineTooLong(t *testing.T) {
	_, err := probe.ParseLocalSnapshots([]byte(strings.Repeat("x", bufio.MaxScanTokenSize+1)))
	if err == nil {
		t.Error("want scanner error for an over-long line")
	}
}

func TestCollectSnapshots(t *testing.T) {
	r := &platform.FakeRunner{Responses: map[string]platform.Result{
		"tmutil listlocalsnapshots /": {Stdout: readFixture(t, "w2_tmutil_snapshots.txt")},
	}}
	s, err := probe.CollectSnapshots(context.Background(), r)
	if err != nil || s.Count != 4 {
		t.Fatalf("s=%+v err=%v", s, err)
	}
	want := []platform.Command{{Name: "tmutil", Args: []string{"listlocalsnapshots", "/"}}}
	if !reflect.DeepEqual(r.Calls, want) {
		t.Errorf("calls = %+v", r.Calls)
	}
}

func TestCollectSnapshotsFailure(t *testing.T) {
	r := &platform.FakeRunner{Responses: map[string]platform.Result{
		"tmutil listlocalsnapshots /": {ExitCode: 80, Stderr: []byte("permission denied\n")},
	}}
	if _, err := probe.CollectSnapshots(context.Background(), r); err == nil || !strings.Contains(err.Error(), "exit status 80: permission denied") {
		t.Errorf("err = %v", err)
	}
	r = &platform.FakeRunner{Errors: map[string]error{"tmutil listlocalsnapshots /": context.Canceled}}
	if _, err := probe.CollectSnapshots(context.Background(), r); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
