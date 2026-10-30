package compiler

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// hundredComponents is an app of 100 components of a realistic size: props,
// signals, a computed value, a keyed list, a conditional, handlers and a
// style block that uses design tokens.
func hundredComponents() []Source {
	var b strings.Builder
	b.WriteString("type Item struct {\n    ID   int\n    Name string\n}\n\n")
	b.WriteString(`const (
    Surface den.Color    = "#ffffff"
    Ink     den.Color    = "oklch(25% 0.02 260)"
    Hover   den.Color    = "rgb(0 0 0 / 0.05)"
    Space2  den.Length   = "8px"
    Space4  den.Length   = "16px"
    Radius  den.Length   = "12px"
    Fast    den.Duration = "150ms"
    Body    den.Font     = "system-ui, sans-serif"
)

`)
	b.WriteString("component App() {\n    <main>\n")
	for i := 0; i < 99; i++ {
		fmt.Fprintf(&b, "        <C%d title=\"c%d\" start={%d} />\n", i, i, i)
	}
	b.WriteString("    </main>\n}\n")
	for i := 0; i < 99; i++ {
		fmt.Fprintf(&b, `
component C%d(title string, start int) {
    n := signal(start)
    items := signal([]Item{{1, "a"}, {2, "b"}})
    total := computed(n.Get() * 2)
    style {
        .card { padding: $space-4; border-radius: $radius; background: $surface; font-family: $body; }
        .card h2 { color: $ink; margin: 0 0 $space-2; }
        li:hover, li:focus-visible { background: $hover; transition: background $fast; }
        @media (min-width: 40rem) { .card { padding: calc($space-4 * 2); } }
    }
    <section class="card">
        <h2>{title}: {n.Get()} / {total.Get()}</h2>
        <ul>
            {for _, it := range items.Get() key it.ID {
                <li onclick={n.Set(n.Get() + it.ID)}>{it.Name}</li>
            }}
        </ul>
        {if n.Get() > 10 {
            <p>big</p>
        } else {
            <p>small</p>
        }}
        <button onclick={items.Set(append(items.Get(), Item{len(items.Get()) + 1, "x"}))}>add</button>
    </section>
}
`, i)
	}
	return []Source{{Name: "app.den", Code: b.String()}}
}

// TestRebuildBudget checks the rebuild budget: a dev rebuild of a
// 100-component app takes under 50 ms. It allows 2x for slow machines, and
// skips under the race detector, which makes timings meaningless.
func TestRebuildBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("timing is not meaningful under the race detector")
	}
	src := hundredComponents()
	if res, err := Compile(src); err != nil || !strings.Contains(res.CSS, ":root{") {
		t.Fatal(err, "or no CSS")
	}
	best := time.Hour
	for i := 0; i < 5; i++ {
		start := time.Now()
		if _, err := Compile(src); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	t.Logf("100 components compile in %v", best)
	if best > 2*50*time.Millisecond {
		t.Errorf("compiling 100 components took %v; the budget is 50 ms", best)
	}
}

func BenchmarkCompile100(b *testing.B) {
	src := hundredComponents()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(src); err != nil {
			b.Fatal(err)
		}
	}
}
