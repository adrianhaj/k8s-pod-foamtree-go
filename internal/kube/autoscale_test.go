package kube

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

func TestAutoscalers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/apis/autoscaling/v2/horizontalpodautoscalers":
			fmt.Fprint(w, `{"apiVersion":"autoscaling/v2","kind":"HorizontalPodAutoscalerList","items":[
				{"metadata":{"name":"web-hpa","namespace":"shop"},"spec":{"scaleTargetRef":{"kind":"Deployment","name":"web"},"maxReplicas":5}}]}`)
		case "/apis/autoscaling.k8s.io/v1/verticalpodautoscalers":
			fmt.Fprint(w, `{"items":[
				{"metadata":{"namespace":"shop"},"spec":{"targetRef":{"name":"api"}},"status":{"recommendation":{"containerRecommendations":[
					{"target":{"cpu":"100m","memory":"64Mi"}},{"target":{"cpu":"20m","memory":"1Mi"}}]}}},
				{"metadata":{"namespace":"shop"},"spec":{"targetRef":{"name":"cpuonly"}},"status":{"recommendation":{"containerRecommendations":[
					{"target":{"cpu":"50m"}}]}}},
				{"metadata":{"namespace":"shop"},"spec":{"targetRef":{"name":"fresh"}},"status":{}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got := (&clusterCache{client: cs}).autoscalers(context.Background())
	want := autoscalers{
		hpa: map[string]string{"shop/web": "web-hpa"},
		// A VPA with no recommendation yet has nothing to draw.
		// A VPA controlling only CPU has no memory target: 0, not a recommendation of none.
		vpa: map[string]foam.Container{"shop/api": {CPU: 120, Memory: 65 << 20}, "shop/cpuonly": {CPU: 50}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAutoscalersKeepOnTransientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	prev := autoscalers{hpa: map[string]string{"shop/web": "web-hpa"}, vpa: map[string]foam.Container{"shop/api": {CPU: 1}}}
	if got := (&clusterCache{client: cs}).listAutoscalers(context.Background(), prev); !reflect.DeepEqual(got, prev) {
		t.Errorf("a 503 dropped the last answer: got %+v", got)
	}
}
