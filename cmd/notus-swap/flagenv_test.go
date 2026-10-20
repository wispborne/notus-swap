package main

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// flagEnv is written out by hand, so check it names every env setting that
// a flag in main.go reads.
func TestFlagEnvMatchesMain(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`flag\.\w+\("([\w-]+)", env(?:Float)?\("(\w+)"`)
	found := re.FindAllStringSubmatch(string(src), -1)
	if len(found) == 0 {
		t.Fatal("no flags read from the env found in main.go")
	}
	for _, m := range found {
		if flagEnv[m[1]] != m[2] {
			t.Errorf("flagEnv[%q] is %q, but main.go reads %s", m[1], flagEnv[m[1]], m[2])
		}
	}
	if len(flagEnv) != len(found) {
		var names []string
		for _, m := range found {
			names = append(names, m[1])
		}
		for k := range flagEnv {
			if !slices.Contains(names, k) {
				t.Errorf("flagEnv has %q, which main.go doesn't read from the env", k)
			}
		}
	}
}
