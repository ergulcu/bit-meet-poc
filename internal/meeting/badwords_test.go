// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package meeting

import (
	"strings"
	"testing"
)

func TestBadWordsMatchWholeWordsOnly(t *testing.T) {
	b := NewBadWords([]string{"yasak", "xxx", "  ", ""})
	if b.Len() != 2 {
		t.Fatalf("Len() = %d, blank lines must be ignored", b.Len())
	}
	tests := []struct {
		text string
		want bool
	}{
		{"Bu konu yasak", true},
		{"YASAK toplantı", true},
		{"yasak.", true},
		{"(xxx)", true},
		{"yasakla ilgili", false}, // part of a longer word
		{"kayasak", false},
		{"xxx1", false},
		{"Sprint planning", false},
		{"", false},
		{"   ", false},
	}
	for _, tt := range tests {
		if _, got := b.FindIn(tt.text); got != tt.want {
			t.Errorf("FindIn(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestBadWordsUseTurkishCase(t *testing.T) {
	b := NewBadWords([]string{"gizli", "küfür"})
	if w, ok := b.FindIn("GİZLİ plan"); !ok || w != "gizli" {
		t.Errorf("GİZLİ: %q, %v", w, ok)
	}
	// With Turkish rules "I" is "ı", so "GIZLI" is "gızlı" and not "gizli".
	if _, ok := b.FindIn("GIZLI"); ok {
		t.Error("GIZLI must not match gizli")
	}
	if _, ok := b.FindIn("KÜFÜR yok"); !ok {
		t.Error("KÜFÜR must match")
	}
}

func TestTheDefaultListIsEmbedded(t *testing.T) {
	b := DefaultBadWords()
	if b.Len() != 4 {
		t.Errorf("the default list has %d words, want 4", b.Len())
	}
	if _, ok := b.FindIn("Gizli toplantı"); !ok {
		t.Error("gizli is in the default list")
	}
}

func TestReadBadWords(t *testing.T) {
	b, err := ReadBadWords(strings.NewReader("bir\r\niki\n\nüç"))
	if err != nil || b.Len() != 3 {
		t.Fatalf("%d words, %v", b.Len(), err)
	}
	if _, ok := b.FindIn("iki kişi"); !ok {
		t.Error("CRLF lines must be read")
	}
}
