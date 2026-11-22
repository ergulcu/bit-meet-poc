// Copyright 2022 Mehmet ERGULCU
// SPDX-License-Identifier: Apache-2.0

package meeting

import (
	"bufio"
	_ "embed"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

//go:embed badwords.txt
var defaultBadWords string

// BadWords finds forbidden words in free text. Words are matched whole and ignoring case with Turkish rules,
// so "YASAK" and "yasak" match "yasak", "Iğdır" is lowered to "ığdır", and "yasakla" does not match "yasak".
type BadWords struct {
	words []string
}

// NewBadWords uses the given words; blank ones are ignored.
func NewBadWords(words []string) *BadWords {
	b := &BadWords{}
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			b.words = append(b.words, lower(w))
		}
	}
	return b
}

// ReadBadWords reads one word per line.
func ReadBadWords(r io.Reader) (*BadWords, error) {
	var words []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		words = append(words, scanner.Text())
	}
	return NewBadWords(words), scanner.Err()
}

// DefaultBadWords is the list that comes with the program.
func DefaultBadWords() *BadWords {
	b, _ := ReadBadWords(strings.NewReader(defaultBadWords))
	return b
}

// Len is the number of words.
func (b *BadWords) Len() int { return len(b.words) }

// FindIn returns the first forbidden word found in the text.
func (b *BadWords) FindIn(text string) (string, bool) {
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	normalized := lower(text)
	for _, w := range b.words {
		for from := 0; from < len(normalized); {
			i := strings.Index(normalized[from:], w)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(w)
			if !wordRuneBefore(normalized, start) && !wordRuneAfter(normalized, end) {
				return w, true
			}
			_, size := utf8.DecodeRuneInString(normalized[start:])
			from = start + size
		}
	}
	return "", false
}

func lower(s string) string { return strings.ToLowerSpecial(unicode.TurkishCase, s) }

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

func wordRuneBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return isWordRune(r)
}

func wordRuneAfter(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return isWordRune(r)
}
