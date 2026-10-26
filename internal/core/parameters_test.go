package core

import "testing"

func TestStopFilterCutsTheOutputAtTheStopSequence(t *testing.T) {
	filter := stopFilter{stops: []string{"STOP"}}
	published, stopped := filter.push("Hello")
	if stopped || published != "He" {
		t.Fatalf("first push = %q stopped=%v", published, stopped)
	}
	published, stopped = filter.push(" STOP world")
	if !stopped || published != "llo " {
		t.Fatalf("second push = %q stopped=%v", published, stopped)
	}
	if published, stopped = filter.push("more"); !stopped || published != "" {
		t.Fatalf("push after the stop = %q stopped=%v", published, stopped)
	}
	if tail := filter.flush(); tail != "" {
		t.Fatalf("flush after the stop = %q", tail)
	}
}

func TestStopFilterKeepsTheTailUntilTheOutputEnds(t *testing.T) {
	filter := stopFilter{stops: []string{"XYZ"}}
	published, stopped := filter.push("abc")
	if stopped || published != "a" {
		t.Fatalf("push = %q stopped=%v", published, stopped)
	}
	if tail := filter.flush(); tail != "bc" {
		t.Fatalf("flush = %q", tail)
	}
}

func TestStopFilterWithoutStopsPublishesEverything(t *testing.T) {
	filter := stopFilter{}
	published, stopped := filter.push("abc")
	if stopped || published != "abc" {
		t.Fatalf("push = %q stopped=%v", published, stopped)
	}
}
