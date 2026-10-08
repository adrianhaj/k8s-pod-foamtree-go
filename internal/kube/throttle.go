package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// throttleQuery is the worst container's throttled share of its CPU periods,
// per pod. The container filter drops the pod-level cgroup series, which
// would count the same periods twice.
const throttleQuery = `max by (namespace,pod) (` +
	`rate(container_cpu_cfs_throttled_periods_total{container!="",container!="POD"}[5m]) / ` +
	`rate(container_cpu_cfs_periods_total{container!="",container!="POD"}[5m]))`

const throttleTimeout = 2 * time.Second

// One series per pod cluster-wide: generous, but a bounded read.
const throttleMaxBytes = 8 << 20

// ponytail: one cache for the whole process, so a second cluster needs its own Throttle
const throttleTTL = time.Minute

// Throttle reads CPU throttling from a Prometheus that scrapes cAdvisor. It is
// optional, so a failed query only costs the throttled finding.
type Throttle struct {
	URL string

	mu     sync.Mutex
	at     time.Time
	shares map[string]float64
}

// Shares returns the throttled share of each pod, keyed "namespace/name".
// Snapshots call it on every refresh, so the answer is cached for throttleTTL,
// failures included: during an outage each refresh would otherwise wait out the timeout.
func (t *Throttle) Shares(ctx context.Context) (map[string]float64, error) {
	t.mu.Lock()
	if time.Since(t.at) < throttleTTL {
		shares := t.shares
		t.mu.Unlock()
		return shares, nil
	}
	// Marked before the query runs, so concurrent snapshots get the old shares rather than queue behind it.
	t.at = time.Now()
	t.mu.Unlock()
	shares, err := t.query(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.shares = shares
	t.mu.Unlock()
	return shares, nil
}

// query runs detached from the caller: one client disconnect must not cancel the refresh every snapshot shares.
func (t *Throttle) query(ctx context.Context) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), throttleTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(t.URL, "/")+"/api/v1/query?"+url.Values{"query": {throttleQuery}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus: %s", resp.Status)
	}
	return parseThrottle(io.LimitReader(resp.Body, throttleMaxBytes))
}

// parseThrottle reads an instant-query vector of per-pod values.
func parseThrottle(r io.Reader) (map[string]float64, error) {
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", body.Error)
	}
	out := make(map[string]float64, len(body.Data.Result))
	for _, s := range body.Data.Result {
		if len(s.Value) != 2 {
			continue
		}
		str, _ := s.Value[1].(string)
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, err
		}
		out[s.Metric["namespace"]+"/"+s.Metric["pod"]] = v
	}
	return out, nil
}
