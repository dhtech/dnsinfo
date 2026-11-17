// SPDX-License-Identifier: GPL-3.0-or-later

package count

import (
	"reflect"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCountingByDomainHourAndResult(t *testing.T) {
	c := New(100)
	c.Add("example.org", "answered", at("2026-01-02 10:00:00"))
	c.Add("example.org", "answered", at("2026-01-02 10:59:59"))
	c.Add("example.org", "noname", at("2026-01-02 10:30:00"))
	c.Add("example.org", "answered", at("2026-01-02 11:00:00"))
	c.Add("example.net", "answered", at("2026-01-02 10:15:00"))
	if c.Held() != 4 {
		t.Fatalf("held %d", c.Held())
	}
	want := []Row{
		{"example.net", at("2026-01-02 10:00:00"), "answered", 1},
		{"example.org", at("2026-01-02 10:00:00"), "answered", 2},
		{"example.org", at("2026-01-02 10:00:00"), "noname", 1},
		{"example.org", at("2026-01-02 11:00:00"), "answered", 1},
	}
	if got := c.Take(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if c.Held() != 0 || len(c.Take()) != 0 {
		t.Fatal("not empty after Take")
	}
}

func TestReturnedRowsAreAddedToNewOnes(t *testing.T) {
	c := New(100)
	c.Add("example.org", "answered", at("2026-01-02 10:00:00"))
	rows := c.Take()
	c.Add("example.org", "answered", at("2026-01-02 10:00:05"))
	c.Return(rows) // the write failed
	got := c.Take()
	if len(got) != 1 || got[0].Answers != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestMoreDomainsThanAnHourMayHoldGoToOther(t *testing.T) {
	c := New(2)
	h := at("2026-01-02 10:00:00")
	for _, d := range []string{"a.example", "b.example", "c.example", "d.example", "a.example"} {
		c.Add(d, "answered", h)
	}
	c.Take()
	c.Add("e.example", "answered", h)                         // still the same hour: the limit holds across writes
	c.Add("b.example", "answered", h)                         // one of the two known ones
	c.Add("e.example", "answered", at("2026-01-02 11:00:00")) // a new hour starts over
	got := map[string]int64{}
	for _, r := range c.Take() {
		got[r.Domain+r.Hour.Format(" 15h")] += r.Answers
	}
	want := map[string]int64{Other + " 10h": 1, "b.example 10h": 1, "e.example 11h": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
