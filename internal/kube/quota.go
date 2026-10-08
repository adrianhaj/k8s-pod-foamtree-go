package kube

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Quota is one ResourceQuota's hard limits and what is used of them. CPU is
// in cores, memory in bytes; a bare "cpu" or "memory" key reads as the
// "requests." one it means.
type Quota struct {
	Namespace string             `json:"namespace"`
	Name      string             `json:"name"`
	Hard      map[string]float64 `json:"hard"`
	Used      map[string]float64 `json:"used"`
}

// Quotas lists every ResourceQuota in a context. Quotas are optional: a
// missing RBAC rule answers none, as a cluster without quotas does.
func (s *Source) Quotas(ctx context.Context, name string) ([]Quota, error) {
	c, err := s.clusterOf(name)
	if err != nil {
		return nil, err
	}
	// ResourceVersion "0" reads the apiserver's watch cache, not etcd.
	list, err := c.client.CoreV1().ResourceQuotas(metav1.NamespaceAll).List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if apierrors.IsForbidden(err) {
		return []Quota{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Quota, 0, len(list.Items))
	for _, q := range list.Items {
		v := Quota{Namespace: q.Namespace, Name: q.Name, Hard: map[string]float64{}, Used: map[string]float64{}}
		for k, h := range q.Status.Hard {
			key := string(k)
			if key == "cpu" || key == "memory" {
				key = "requests." + key
			}
			used := q.Status.Used[k]
			v.Hard[key], v.Used[key] = h.AsApproximateFloat64(), used.AsApproximateFloat64()
		}
		out = append(out, v)
	}
	return out, nil
}
