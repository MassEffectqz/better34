package internal

import (
	"strings"
	"testing"
)

func TestLocalSemanticTranslate(t *testing.T) {
	tests := []struct {
		input    string
		contains []string
	}{
		{"котики в шляпах", []string{"cat", "hat"}},
		{"девушка под дождем", []string{"1girl", "rain", "outdoors"}},
		{"горничная с ушками", []string{"maid"}},
		{"1girl beach swimsuit", []string{"1girl", "beach", "swimsuit"}},
		{"unknown gibberish 123456", []string{}},
	}

	for _, tt := range tests {
		res := LocalSemanticTranslate(tt.input)
		tagList := strings.Fields(res)
		tagSet := make(map[string]bool)
		for _, tag := range tagList {
			tagSet[tag] = true
		}
		for _, expectedTag := range tt.contains {
			if !tagSet[expectedTag] {
				t.Errorf("LocalSemanticTranslate(%q) = %q; expected to contain tag %q", tt.input, res, expectedTag)
			}
		}
	}
}
