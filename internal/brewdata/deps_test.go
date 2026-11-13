package brewdata

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type mapRunner map[string]string

func (m mapRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if v, ok := m[strings.Join(args, " ")]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("unexpected call: " + strings.Join(args, " "))
}

func formula(name, deps string, installed bool) string {
	inst := "[]"
	if installed {
		inst = `[{"version":"1"}]`
	}
	return `{"name":"` + name + `","tap":"homebrew/core","versions":{"stable":"1.0"},"dependencies":[` + deps + `],"installed":` + inst + `}`
}

func infoOf(parts ...string) string {
	return `{"formulae":[` + strings.Join(parts, ",") + `],"casks":[]}`
}

func TestExpandDepsFollowsTheTree(t *testing.T) {
	r := mapRunner{
		"info --json=v2 b c": infoOf(formula("b", `"d"`, false), formula("c", "", true)),
		"info --json=v2 d":   infoOf(formula("d", "", false)),
	}
	root := []Package{{Name: "a", Dependencies: []string{"b", "c"}}}
	extra, via, err := ExpandDeps(context.Background(), r, root)
	if err != nil || len(extra) != 2 || extra[0].Name != "b" || extra[1].Name != "d" {
		t.Fatalf("%+v %v", extra, err)
	}
	if via["b"] != "a" || via["d"] != "b" {
		t.Fatalf("via: %v", via)
	}
	if _, ok := via["c"]; ok {
		t.Fatalf("an installed dependency is not extra: %v", via)
	}
}

func TestExpandDepsNothingToDo(t *testing.T) {
	extra, _, err := ExpandDeps(context.Background(), mapRunner{}, []Package{{Name: "a"}})
	if err != nil || len(extra) != 0 {
		t.Fatalf("%v %v", extra, err)
	}
}

func TestExpandDepsErrors(t *testing.T) {
	root := []Package{{Name: "a", Dependencies: []string{"user/tap/x"}}}
	if _, _, err := ExpandDeps(context.Background(), mapRunner{}, root); err == nil {
		t.Fatal("a dependency name brew-guard cannot pass on must be an error")
	}
	big := Package{Name: "a"}
	for i := 0; i <= maxDeps; i++ {
		big.Dependencies = append(big.Dependencies, "p"+strings.Repeat("x", i%50)+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+string(rune('a'+(i/676)%26)))
	}
	if _, _, err := ExpandDeps(context.Background(), mapRunner{}, []Package{big}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("a huge tree must stop: %v", err)
	}
}
