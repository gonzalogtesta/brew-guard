package brewdata

import (
	"context"
	"fmt"
)

const maxDeps = 500

// ExpandDeps finds the dependencies that are not installed yet and that the
// given packages need, following them until nothing new turns up. It reads
// brew's formula metadata (`brew info --json=v2`), not brew's own plan, so it
// is an approximation: build-only dependencies are not included and casks are
// not expanded. via maps each extra package to the package that needs it.
func ExpandDeps(ctx context.Context, r Runner, pkgs []Package) (extra []Package, via map[string]string, err error) {
	via = map[string]string{}
	seen := map[string]bool{}
	for _, p := range pkgs {
		seen[p.Name] = true
	}
	level := pkgs
	for len(level) > 0 {
		var want []string
		for _, p := range level {
			for _, d := range p.Dependencies {
				if seen[d] {
					continue
				}
				seen[d] = true
				via[d] = p.Name
				want = append(want, d)
			}
		}
		if len(want) == 0 {
			break
		}
		if len(seen) > maxDeps {
			return nil, nil, fmt.Errorf("more than %d packages in the dependency tree", maxDeps)
		}
		next, err := Info(ctx, r, want...)
		if err != nil {
			return nil, nil, err
		}
		level = level[:0:0]
		for _, p := range next {
			if !p.Installed {
				extra = append(extra, p)
				level = append(level, p)
			} else {
				delete(via, p.Name)
			}
		}
	}
	return extra, via, nil
}
