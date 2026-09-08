package metrics

import (
	"strings"
	"testing"
)

func TestCounterAndGaugeRender(t *testing.T) {
	r := New()
	r.CounterInc("tw_requests_total", "Requests.", LabelSet{"code": "200", "method": "GET"})
	r.CounterInc("tw_requests_total", "Requests.", LabelSet{"code": "200", "method": "GET"})
	r.CounterInc("tw_requests_total", "Requests.", LabelSet{"code": "404", "method": "GET"})
	r.GaugeSet("tw_active", "Active users.", 1, LabelSet{"status": "active"})
	r.GaugeSet("tw_active", "Active users.", 1, LabelSet{"status": "active"})

	out := string(r.Prometheus())

	want := `# HELP tw_requests_total Requests.
# TYPE tw_requests_total counter
tw_requests_total{code="200",method="GET"} 2
tw_requests_total{code="404",method="GET"} 1
# HELP tw_active Active users.
# TYPE tw_active gauge
tw_active{status="active"} 1
`
	if out != want {
		t.Fatalf("unexpected output:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestUnlabelledSeries(t *testing.T) {
	r := New()
	r.CounterInc("tw_plain", "No labels.", nil)

	out := string(r.Prometheus())
	if !strings.Contains(out, "tw_plain 1\n") {
		t.Fatalf("expected unlabelled counter line, got:\n%s", out)
	}
}

func TestHistogramRender(t *testing.T) {
	r := New()
	r.HistogramObserve("tw_dur_seconds", "Duration.", 0.05, LabelSet{"handler": "v1"}, []float64{0.01, 0.1, 1})
	r.HistogramObserve("tw_dur_seconds", "Duration.", 0.5, LabelSet{"handler": "v1"}, []float64{0.01, 0.1, 1})

	out := string(r.Prometheus())
	want := `# HELP tw_dur_seconds Duration.
# TYPE tw_dur_seconds histogram
tw_dur_seconds_bucket{handler="v1",le="0.01"} 0
tw_dur_seconds_bucket{handler="v1",le="0.1"} 1
tw_dur_seconds_bucket{handler="v1",le="1"} 2
tw_dur_seconds_sum{handler="v1"} 0.55
tw_dur_seconds_count{handler="v1"} 2
`
	if out != want {
		t.Fatalf("unexpected output:\ngot:\n%s\nwant:\n%s", out, want)
	}
}

func TestDefaultBucketsApplied(t *testing.T) {
	r := New()
	r.HistogramObserve("tw_dur_seconds", "Duration.", 0.001, nil, nil)

	out := string(r.Prometheus())
	if !strings.Contains(out, `le="0.005"`) {
		t.Fatalf("expected default smallest bucket, got:\n%s", out)
	}
}

func TestLabelValuesEscapedWithQuotes(t *testing.T) {
	r := New()
	r.CounterInc("tw_weird", "Weird.", LabelSet{"reason": `said "hi"`})

	out := string(r.Prometheus())
	if !strings.Contains(out, `reason="said \"hi\""`) {
		t.Fatalf("expected escaped label value, got:\n%s", out)
	}
}

func TestDeterministicOrdering(t *testing.T) {
	r := New()
	r.GaugeSet("z_metric", "Z.", 1, nil)
	r.GaugeSet("a_metric", "A.", 1, nil)
	r.CounterInc("m_metric", "M.", nil)

	first := string(r.Prometheus())
	second := string(r.Prometheus())
	if first != second {
		t.Fatalf("output not deterministic:\n%s\n---\n%s", first, second)
	}
}
