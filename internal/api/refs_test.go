package api

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseRefFilters(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   []string
		want []RefFilter
		bad  bool
	}{
		{name: "none", in: nil, want: []RefFilter{}},
		{name: "one", in: []string{"spaceman.run:run_a"}, want: []RefFilter{{"spaceman.run", "run_a"}}},
		{name: "colons in value", in: []string{"git.repo:https://x.example:443/a"}, want: []RefFilter{{"git.repo", "https://x.example:443/a"}}},
		{name: "four", in: []string{"a:1", "b:2", "c:3", "d:4"}, want: []RefFilter{{"a", "1"}, {"b", "2"}, {"c", "3"}, {"d", "4"}}},
		{name: "max value", in: []string{"k:" + strings.Repeat("v", MaxRefValue)}, want: []RefFilter{{"k", strings.Repeat("v", MaxRefValue)}}},
		{name: "five", in: []string{"a:1", "b:2", "c:3", "d:4", "e:5"}, bad: true},
		{name: "no colon", in: []string{"nocolon"}, bad: true},
		{name: "empty key", in: []string{":v"}, bad: true},
		{name: "upper key", in: []string{"Bad.Key:v"}, bad: true},
		{name: "empty value", in: []string{"k:"}, bad: true},
		{name: "long value", in: []string{"k:" + strings.Repeat("v", MaxRefValue+1)}, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRefFilters(tt.in)
			if tt.bad {
				var ae *Error
				if !errors.As(err, &ae) || ae.Code() != "validation_failed" {
					t.Fatalf("want validation error, got %v", err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v %v, want %v", got, err, tt.want)
			}
		})
	}
}
