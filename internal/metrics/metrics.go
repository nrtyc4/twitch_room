// Package metrics is a minimal, dependency-free metrics registry that emits
// the Prometheus text exposition format. It supports the three series types
// Prometheus understands - counters, gauges and histograms - with arbitrary
// label sets, which is enough to feed a scraper without pulling in the full
// client library.
package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// LabelSet pairs a label name with its value. Values are escaped when
// rendered, so callers can pass arbitrary strings.
type LabelSet map[string]string

// DefaultDurationBuckets are the histogram bucket boundaries used for request
// durations unless an explicit set is given.
var DefaultDurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry collects metric samples under a lock and serialises them in
// Prometheus format. All methods are safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	counters map[string]*counter
	gauges   map[string]*gauge
	hists    map[string]*histogram
}

type counter struct {
	help   string
	values map[string]float64
}

type gauge struct {
	help   string
	values map[string]float64
}

type histogram struct {
	help    string
	buckets []float64
	counts  map[string][]float64 // label signature -> cumulative bucket counts
	sum     map[string]float64   // label signature -> sum of observed values
	count   map[string]float64   // label signature -> number of observations
}

func New() *Registry {
	return &Registry{
		counters: make(map[string]*counter),
		gauges:   make(map[string]*gauge),
		hists:    make(map[string]*histogram),
	}
}

// CounterInc increments a counter by one, creating it on first use.
func (r *Registry) CounterInc(name, help string, labels LabelSet) {
	r.CounterAdd(name, help, 1, labels)
}

// CounterAdd adds delta to a counter, creating it on first use.
func (r *Registry) CounterAdd(name, help string, delta float64, labels LabelSet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.counters[name]
	if !ok {
		c = &counter{help: help, values: make(map[string]float64)}
		r.counters[name] = c
	}
	c.values[signature(labels)] += delta
}

// GaugeSet sets a gauge to value, creating it on first use.
func (r *Registry) GaugeSet(name, help string, value float64, labels LabelSet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gauges[name]
	if !ok {
		g = &gauge{help: help, values: make(map[string]float64)}
		r.gauges[name] = g
	}
	g.values[signature(labels)] = value
}

// HistogramObserve records a single observation into the histogram's buckets,
// creating the histogram on first use with the given explicit buckets (or the
// package defaults when none are supplied).
func (r *Registry) HistogramObserve(name, help string, value float64, labels LabelSet, buckets []float64) {
	if len(buckets) == 0 {
		buckets = DefaultDurationBuckets
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.hists[name]
	if !ok {
		h = &histogram{
			help:    help,
			buckets: append([]float64(nil), buckets...),
			counts:  make(map[string][]float64),
			sum:     make(map[string]float64),
			count:   make(map[string]float64),
		}
		r.hists[name] = h
	}
	sig := signature(labels)
	if _, ok := h.counts[sig]; !ok {
		h.counts[sig] = make([]float64, len(h.buckets))
	}
	for i, b := range h.buckets {
		if value <= b {
			h.counts[sig][i]++
		}
	}
	h.sum[sig] += value
	h.count[sig]++
}

// Prometheus renders the registry in Prometheus text format (version 0.0.4).
func (r *Registry) Prometheus() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	var b strings.Builder
	names := sortedKeys(r.counters)
	for _, name := range names {
		c := r.counters[name]
		fmt.Fprintf(&b, "# HELP %s %s\n", name, c.help)
		fmt.Fprintf(&b, "# TYPE %s counter\n", name)
		for _, sig := range sortedKeys(c.values) {
			writeSeries(&b, name, c.values[sig], sig)
		}
	}

	names = sortedKeys(r.gauges)
	for _, name := range names {
		g := r.gauges[name]
		fmt.Fprintf(&b, "# HELP %s %s\n", name, g.help)
		fmt.Fprintf(&b, "# TYPE %s gauge\n", name)
		for _, sig := range sortedKeys(g.values) {
			writeSeries(&b, name, g.values[sig], sig)
		}
	}

	names = sortedKeys(r.hists)
	for _, name := range names {
		h := r.hists[name]
		fmt.Fprintf(&b, "# HELP %s %s\n", name, h.help)
		fmt.Fprintf(&b, "# TYPE %s histogram\n", name)
		for _, sig := range sortedKeys(h.counts) {
			counts := h.counts[sig]
			for i, count := range counts {
				le := formatFloat(h.buckets[i])
				bucketLabels := labelParts(leName(sig, "le", le))
				fmt.Fprintf(&b, "%s_bucket%s %s\n", name, bucketLabels, formatFloat(count))
			}
			extra := labelParts(sig)
			fmt.Fprintf(&b, "%s_sum%s %s\n", name, extra, formatFloat(h.sum[sig]))
			fmt.Fprintf(&b, "%s_count%s %s\n", name, extra, formatFloat(h.count[sig]))
		}
	}

	return []byte(b.String())
}

// writeSeries writes `name{labels} value` or `name value` when label-free.
func writeSeries(b *strings.Builder, name string, value float64, sig string) {
	if sig == "" {
		fmt.Fprintf(b, "%s %s\n", name, formatFloat(value))
		return
	}
	fmt.Fprintf(b, "%s%s %s\n", name, labelParts(sig), formatFloat(value))
}

// signature produces a stable, non-colliding key for a label set.
func signature(labels LabelSet) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, 0, len(labels))
	for k := range labels {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, k+"="+labels[k])
	}
	return strings.Join(parts, ";")
}

// labelParts renders a label signature as `{k="v",k2="v2"}` or "" when empty.
// leName merges an extra histogram-boundary label into the label set.
func labelParts(sig string) string {
	if sig == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	first := true
	// sig is encoded as k=v;k=v with no literal ';' or '=' in values.
	for _, pair := range strings.Split(sig, ";") {
		if !first {
			b.WriteByte(',')
		}
		first = false
		kv := strings.SplitN(pair, "=", 2)
		fmt.Fprintf(&b, "%s=%q", kv[0], kv[1])
	}
	b.WriteByte('}')
	return b.String()
}

func leName(sig, key, value string) string {
	if sig == "" {
		return key + "=" + value
	}
	return sig + ";" + key + "=" + value
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
