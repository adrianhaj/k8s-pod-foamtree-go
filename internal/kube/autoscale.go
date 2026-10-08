package kube

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

// Autoscalers change slowly, and every refresh asks twice (CPU, then memory).
const autoscaleTTL = time.Minute

const autoscaleTimeout = 5 * time.Second

// autoscalers maps "namespace/workload" to the HPA scaling it and the VPA's summed target.
// ponytail: keyed by name, not kind, so a Deployment and a StatefulSet sharing a name in one namespace collide
type autoscalers struct {
	hpa map[string]string
	vpa map[string]foam.Container
}

// autoscaleCache holds one cluster's autoscalers. Failures are cached too, so
// a cluster without the VPA CRD is not asked on every refresh.
type autoscaleCache struct {
	mu  sync.Mutex
	at  time.Time
	val autoscalers
}

func (c *clusterCache) autoscalers(ctx context.Context) autoscalers {
	a := &c.scalers
	a.mu.Lock()
	if time.Since(a.at) < autoscaleTTL {
		val := a.val
		a.mu.Unlock()
		return val
	}
	// Marked before the lists run, so concurrent snapshots get the old answer rather than queue behind them.
	a.at = time.Now()
	a.mu.Unlock()
	val := c.listAutoscalers(ctx)
	a.mu.Lock()
	a.val = val
	a.mu.Unlock()
	return val
}

// listAutoscalers runs detached: one client disconnect must not cache an empty answer for everyone.
func (c *clusterCache) listAutoscalers(ctx context.Context) autoscalers {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autoscaleTimeout)
	defer cancel()
	// Optional, so a missing API or RBAC rule only hides the marks.
	quiet := func(what string, err error) {
		if !apierrors.IsNotFound(err) && !apierrors.IsForbidden(err) {
			slog.Warn(what, "err", err)
		}
	}
	val := autoscalers{hpa: map[string]string{}}
	// ResourceVersion "0" reads the apiserver's watch cache, not etcd.
	hpas, err := c.client.AutoscalingV2().HorizontalPodAutoscalers(metav1.NamespaceAll).List(ctx, metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		quiet("hpa", err)
	} else {
		for _, h := range hpas.Items {
			val.hpa[h.Namespace+"/"+h.Spec.ScaleTargetRef.Name] = h.Name
		}
	}
	// A fake clientset has no REST client to ask.
	if rc, ok := c.client.CoreV1().RESTClient().(*rest.RESTClient); ok && rc != nil {
		b, err := rc.Get().AbsPath("/apis/autoscaling.k8s.io/v1/verticalpodautoscalers").DoRaw(ctx)
		if err == nil {
			val.vpa, err = parseVPA(b)
		}
		if err != nil {
			quiet("vpa", err)
		}
	}
	return val
}

// parseVPA sums each VPA's per-container targets, keyed by its target workload.
func parseVPA(b []byte) (map[string]foam.Container, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				TargetRef struct {
					Name string `json:"name"`
				} `json:"targetRef"`
			} `json:"spec"`
			Status struct {
				Recommendation struct {
					ContainerRecommendations []struct {
						Target struct {
							CPU    resource.Quantity `json:"cpu"`
							Memory resource.Quantity `json:"memory"`
						} `json:"target"`
					} `json:"containerRecommendations"`
				} `json:"recommendation"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := map[string]foam.Container{}
	for _, v := range list.Items {
		recs := v.Status.Recommendation.ContainerRecommendations
		if len(recs) == 0 {
			continue
		}
		var t foam.Container
		for _, r := range recs {
			t.CPU += r.Target.CPU.MilliValue()
			t.Memory += r.Target.Memory.Value()
		}
		out[v.Metadata.Namespace+"/"+v.Spec.TargetRef.Name] = t
	}
	return out, nil
}
