package kube

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestQuotas(t *testing.T) {
	rl := func(kv ...string) corev1.ResourceList {
		out := corev1.ResourceList{}
		for i := 0; i < len(kv); i += 2 {
			out[corev1.ResourceName(kv[i])] = resource.MustParse(kv[i+1])
		}
		return out
	}
	quota := func(name string, hard, used corev1.ResourceList) *corev1.ResourceQuota {
		return &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "shop"},
			Status: corev1.ResourceQuotaStatus{Hard: hard, Used: used}}
	}
	cs := fake.NewClientset(
		quota("compute", rl("cpu", "4", "requests.cpu", "3", "limits.memory", "8Gi"), rl("cpu", "1500m", "requests.cpu", "1500m", "limits.memory", "2Gi")),
		quota("objects", rl("pods", "10"), rl("pods", "9")),
	)
	got, err := testSource(t, cs).Quotas(context.Background(), "kind-b")
	if err != nil {
		t.Fatal(err)
	}
	want := []Quota{
		{Namespace: "shop", Name: "compute", Hard: map[string]float64{"requests.cpu": 3, "limits.memory": 8 << 30},
			Used: map[string]float64{"requests.cpu": 1.5, "limits.memory": 2 << 30}},
		{Namespace: "shop", Name: "objects", Hard: map[string]float64{"pods": 10}, Used: map[string]float64{"pods": 9}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("quotas = %+v, want %+v", got, want)
	}

	denied := fake.NewClientset()
	denied.PrependReactor("list", "resourcequotas", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "resourcequotas"}, "", nil)
	})
	if got, err := testSource(t, denied).Quotas(context.Background(), "kind-b"); err != nil || len(got) != 0 {
		t.Errorf("forbidden: %v, %v; want none and no error", got, err)
	}
}
