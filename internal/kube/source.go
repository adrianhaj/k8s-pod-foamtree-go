// Package kube keeps one watch-backed cache of nodes and pods per cluster, so
// a dashboard refresh reads memory instead of listing the whole cluster.
package kube

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/adrianhaj/k8s-pod-foamtree-go/internal/foam"
)

// InCluster is the only context name offered when running inside a pod.
const InCluster = "in-cluster"

var ErrUnknownContext = errors.New("unknown context")

// Terminated pods keep their requests in the API but reserve nothing.
const activePods = "status.phase!=Succeeded,status.phase!=Failed"

type LogRequest struct {
	Namespace, Pod, Container string
	Tail                      int64
	Previous                  bool
}

// MaxLogBytes caps one fetch: a tail of long lines can still be megabytes.
const MaxLogBytes = 1 << 20

// ponytail: the API cuts limitBytes from the start, so the 1 MiB cap is applied here; 32 MiB bounds the read
const fetchLimitBytes = 32 << 20

type Context struct {
	Context string `json:"context"`
	Active  bool   `json:"active"`
}

type Source struct {
	inCluster bool
	rules     *clientcmd.ClientConfigLoadingRules
	newClient func(context string) (kubernetes.Interface, error)

	mu     sync.Mutex
	caches map[string]*clusterCache
}

// NewSource reads ~/.kube/config (or $KUBECONFIG) on every call, like kubectl,
// and never writes it. inCluster uses the pod's service account instead.
func NewSource(inCluster bool) *Source {
	s := &Source{inCluster: inCluster, rules: clientcmd.NewDefaultClientConfigLoadingRules(), caches: map[string]*clusterCache{}}
	s.newClient = s.client
	return s
}

func (s *Source) Contexts() ([]Context, error) {
	if s.inCluster {
		return []Context{{Context: InCluster, Active: true}}, nil
	}
	cfg, err := s.rules.Load()
	if err != nil {
		return nil, err
	}
	out := []Context{}
	for name := range cfg.Contexts {
		out = append(out, Context{Context: name, Active: name == cfg.CurrentContext})
	}
	slices.SortFunc(out, func(a, b Context) int { return cmp.Compare(a.Context, b.Context) })
	return out, nil
}

// resolve maps "" to the active context and rejects names not in kubeconfig.
func (s *Source) resolve(name string) (string, error) {
	contexts, err := s.Contexts()
	if err != nil {
		return "", err
	}
	for _, c := range contexts {
		if (name == "" && c.Active) || c.Context == name {
			return c.Context, nil
		}
	}
	return "", fmt.Errorf("%w %q", ErrUnknownContext, name)
}

func (s *Source) client(name string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	if s.inCluster {
		cfg, err = rest.InClusterConfig()
	} else {
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(s.rules, &clientcmd.ConfigOverrides{CurrentContext: name}).ClientConfig()
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// Snapshot returns the cached nodes and active pods of a context, starting its
// watches on first use and waiting (bounded by ctx) for the initial list.
func (s *Source) Snapshot(ctx context.Context, name string) ([]foam.Node, []foam.Pod, error) {
	name, err := s.resolve(name)
	if err != nil {
		return nil, nil, err
	}
	c, err := s.cache(name)
	if err != nil {
		return nil, nil, err
	}
	if err := c.wait(ctx); err != nil {
		if !c.nodes.HasSynced() || !c.pods.HasSynced() {
			s.evict(name, c)
		}
		return nil, nil, fmt.Errorf("cluster %q: %w", name, err)
	}
	nodes := []foam.Node{}
	for _, o := range c.nodes.GetStore().List() {
		nodes = append(nodes, foam.FromNode(o.(*corev1.Node)))
	}
	pods := []foam.Pod{}
	for _, o := range c.pods.GetStore().List() {
		pods = append(pods, foam.FromPod(o.(*corev1.Pod)))
	}
	return nodes, pods, nil
}

// Logs returns the newest MaxLogBytes of one container's logs through the
// context's cached client, starting its watches if this is the first request
// for that context.
func (s *Source) Logs(ctx context.Context, name string, req LogRequest) (io.ReadCloser, error) {
	name, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	c, err := s.cache(name)
	if err != nil {
		return nil, err
	}
	limit := int64(fetchLimitBytes)
	rc, err := c.client.CoreV1().Pods(req.Namespace).GetLogs(req.Pod, &corev1.PodLogOptions{
		Container: req.Container, TailLines: &req.Tail, Previous: req.Previous, LimitBytes: &limit,
	}).Stream(ctx)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := tail(rc, MaxLogBytes)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// tail reads r to the end holding at most about 2*limit bytes, and returns the newest limit.
func tail(r io.Reader, limit int) ([]byte, error) {
	chunk := make([]byte, 32<<10)
	buf := make([]byte, 0, 2*limit+len(chunk))
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > 2*limit {
			buf = append(buf[:0], buf[len(buf)-limit-1:]...)
		}
		if err == io.EOF {
			return LastBytes(buf, limit), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// LastBytes keeps the newest limit bytes, starting at a line boundary unless
// that would drop the last line.
func LastBytes(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	cut := len(b) - limit
	out := b[cut:]
	if b[cut-1] != '\n' {
		if i := bytes.IndexByte(out, '\n'); i >= 0 && i+1 < len(out) {
			out = out[i+1:]
		}
	}
	return out
}

type clusterCache struct {
	client      kubernetes.Interface
	nodes, pods cache.SharedIndexInformer
	lastErr     atomic.Pointer[error]
	cancel      context.CancelFunc
}

// wait returns once both caches hold the initial list, or early with the
// list/watch error so a bad credential fails fast instead of timing out.
func (c *clusterCache) wait(ctx context.Context) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if c.nodes.HasSynced() && c.pods.HasSynced() {
			return nil
		}
		if last := c.lastErr.Load(); last != nil {
			return *last
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("still loading: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// ponytail: post-sync staleness (cluster recreated behind the same context
// name after a successful sync) isn't handled; a restart picks it up.
func (s *Source) cache(name string) (*clusterCache, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.caches[name]; ok {
		return c, nil
	}
	cs, err := s.newClient(name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &clusterCache{
		client: cs,
		nodes:  coreinformers.NewNodeInformer(cs, 0, cache.Indexers{}),
		pods: coreinformers.NewFilteredPodInformer(cs, metav1.NamespaceAll, 0, cache.Indexers{},
			func(o *metav1.ListOptions) { o.FieldSelector = activePods }),
		cancel: cancel,
	}
	remember := func(_ context.Context, _ *cache.Reflector, err error) { c.lastErr.Store(&err) }
	for _, inf := range []cache.SharedIndexInformer{c.nodes, c.pods} {
		if err := inf.SetWatchErrorHandlerWithContext(remember); err != nil {
			cancel()
			return nil, err
		}
	}
	if err := c.nodes.SetTransform(slimNode); err != nil {
		cancel()
		return nil, err
	}
	if err := c.pods.SetTransform(slimPod); err != nil {
		cancel()
		return nil, err
	}
	go c.nodes.RunWithContext(ctx)
	go c.pods.RunWithContext(ctx)
	s.caches[name] = c
	return c, nil
}

// evict drops a cache that failed its first sync and stops its informers, so
// the next Snapshot rebuilds from a fresh client instead of replaying the
// same stuck error forever.
func (s *Source) evict(name string, c *clusterCache) {
	s.mu.Lock()
	if s.caches[name] == c {
		delete(s.caches, name)
	}
	s.mu.Unlock()
	c.cancel()
}

// slimPod keeps only what the dashboard and the simulators read. Env, volumes
// and managedFields dominate pod size, so this is most of the cache's memory
// on a big cluster.
func slimPod(obj any) (any, error) {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return obj, nil // DeletedFinalStateUnknown tombstones pass through
	}
	var owners []metav1.OwnerReference
	if ref := metav1.GetControllerOf(p); ref != nil {
		owners = []metav1.OwnerReference{*ref}
	}
	var affinity *corev1.Affinity
	if a := p.Spec.Affinity; a != nil && a.NodeAffinity != nil && a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
		affinity = &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace, UID: p.UID, ResourceVersion: p.ResourceVersion,
			Labels: p.Labels, OwnerReferences: owners},
		Spec: corev1.PodSpec{
			NodeName:       p.Spec.NodeName,
			Containers:     slimContainers(p.Spec.Containers),
			InitContainers: slimContainers(p.Spec.InitContainers),
			Overhead:       p.Spec.Overhead,
			Resources:      p.Spec.Resources,
			NodeSelector:   p.Spec.NodeSelector,
			Affinity:       affinity,
			Tolerations:    p.Spec.Tolerations,
		},
		Status: corev1.PodStatus{QOSClass: p.Status.QOSClass, Phase: p.Status.Phase,
			Conditions: slimConditions(p.Status.Conditions),
			// ponytail: the kubelet fills these for every running pod on 1.33+ (~few KB/pod); if cache memory measures high,
			// keep them only while a PodResizePending condition is set or when they differ from spec.
			AllocatedResources:    p.Status.AllocatedResources,
			Resources:             p.Status.Resources,
			ContainerStatuses:     slimStatuses(p.Status.ContainerStatuses),
			InitContainerStatuses: slimStatuses(p.Status.InitContainerStatuses)},
	}, nil
}

func slimContainers(cs []corev1.Container) []corev1.Container {
	out := make([]corev1.Container, 0, len(cs))
	for _, c := range cs {
		out = append(out, corev1.Container{Name: c.Name, Resources: c.Resources, RestartPolicy: c.RestartPolicy})
	}
	return out
}

// slimConditions keeps the two conditions the dashboard reads: why the
// scheduler refused the pod, and a resize the kubelet has not applied (an
// Infeasible one makes the scheduler count allocated, not spec, resources).
func slimConditions(cs []corev1.PodCondition) []corev1.PodCondition {
	var out []corev1.PodCondition
	for _, c := range cs {
		if (c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse) || c.Type == corev1.PodResizePending {
			out = append(out, corev1.PodCondition{Type: c.Type, Status: c.Status, Reason: c.Reason, Message: c.Message})
		}
	}
	return out
}

// slimStatuses keeps what the crash findings and the scheduler formula (allocated/actuated resources) read: no images, IDs or messages.
func slimStatuses(cs []corev1.ContainerStatus) []corev1.ContainerStatus {
	if len(cs) == 0 {
		return nil
	}
	term := func(t *corev1.ContainerStateTerminated) *corev1.ContainerStateTerminated {
		if t == nil {
			return nil
		}
		return &corev1.ContainerStateTerminated{Reason: t.Reason, ExitCode: t.ExitCode}
	}
	out := make([]corev1.ContainerStatus, 0, len(cs))
	for _, c := range cs {
		s := corev1.ContainerStatus{Name: c.Name, Ready: c.Ready, RestartCount: c.RestartCount,
			AllocatedResources: c.AllocatedResources, Resources: c.Resources}
		if w := c.State.Waiting; w != nil {
			s.State.Waiting = &corev1.ContainerStateWaiting{Reason: w.Reason}
		}
		s.State.Terminated = term(c.State.Terminated)
		s.LastTerminationState.Terminated = term(c.LastTerminationState.Terminated)
		out = append(out, s)
	}
	return out
}

func slimNode(obj any) (any, error) {
	n, ok := obj.(*corev1.Node)
	if !ok {
		return obj, nil
	}
	conds := make([]corev1.NodeCondition, 0, len(n.Status.Conditions))
	for _, c := range n.Status.Conditions {
		conds = append(conds, corev1.NodeCondition{Type: c.Type, Status: c.Status})
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: n.Name, UID: n.UID, ResourceVersion: n.ResourceVersion, Labels: n.Labels},
		Spec:       corev1.NodeSpec{Unschedulable: n.Spec.Unschedulable, Taints: n.Spec.Taints},
		Status:     corev1.NodeStatus{Capacity: n.Status.Capacity, Allocatable: n.Status.Allocatable, Conditions: conds},
	}, nil
}
