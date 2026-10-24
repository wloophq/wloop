package setup

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeHome struct {
	calls []string
	fail  string
}

func (f *fakeHome) Check(_ context.Context, home string) ([]Finding, error) {
	f.calls = append(f.calls, "check "+home)
	if home == f.fail {
		return nil, errors.New("broken " + home)
	}
	return []Finding{{Home: home, Entry: "hook"}}, nil
}

func (f *fakeHome) Install(_ context.Context, home string, dryRun bool) ([]string, error) {
	f.calls = append(f.calls, "install "+home)
	if home == f.fail {
		return []string{"half " + home}, errors.New("broken " + home)
	}
	if dryRun {
		return []string{"would add " + home}, nil
	}
	return []string{"added " + home}, nil
}

func (f *fakeHome) Remove(_ context.Context, home string, dryRun bool) ([]string, error) {
	f.calls = append(f.calls, "remove "+home)
	return []string{"removed " + home}, nil
}

func TestServiceVisitsEveryHomeInOrder(t *testing.T) {
	h := &fakeHome{}
	s := Service{Home: h}
	lines, err := s.Install(context.Background(), []string{"/a", "/b"}, true)
	if err != nil || !slices.Equal(lines, []string{"would add /a", "would add /b"}) {
		t.Fatalf("%q %v", lines, err)
	}
	findings, err := s.Check(context.Background(), []string{"/a", "/b"})
	if err != nil || len(findings) != 2 || findings[1].Home != "/b" {
		t.Fatalf("%+v %v", findings, err)
	}
	if lines, err := s.Remove(context.Background(), []string{"/a"}, false); err != nil || !slices.Equal(lines, []string{"removed /a"}) {
		t.Fatalf("%q %v", lines, err)
	}
}

func TestAFailingHomeDoesNotStopTheOthers(t *testing.T) {
	h := &fakeHome{fail: "/a"}
	s := Service{Home: h}
	lines, err := s.Install(context.Background(), []string{"/a", "/b"}, false)
	if err == nil || err.Error() != "/a: broken /a" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(lines, []string{"half /a", "added /b"}) {
		t.Fatalf("%q", lines)
	}
	if _, err := s.Check(context.Background(), []string{"/a", "/b"}); err == nil || !slices.Contains(h.calls, "check /b") {
		t.Fatalf("err = %v, calls %q", err, h.calls)
	}
}
