package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	tsv1alpha1 "github.com/akyriako/typesense-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Record all non-status mutations, including failed attempts and transient
// changes that could be hidden by inspecting only the final replica count.
type quorumMutationClient struct {
	client.Client
	mutations   []string
	failPodList bool
}

func (c *quorumMutationClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.mutations = append(c.mutations, fmt.Sprintf("create %T %s", obj, obj.GetName()))
	return c.Client.Create(ctx, obj, opts...)
}

func (c *quorumMutationClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.mutations = append(c.mutations, fmt.Sprintf("update %T %s", obj, obj.GetName()))
	return c.Client.Update(ctx, obj, opts...)
}

func (c *quorumMutationClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.mutations = append(c.mutations, fmt.Sprintf("patch %T %s", obj, obj.GetName()))
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *quorumMutationClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.mutations = append(c.mutations, fmt.Sprintf("delete %T %s", obj, obj.GetName()))
	return c.Client.Delete(ctx, obj, opts...)
}

func (c *quorumMutationClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	c.mutations = append(c.mutations, fmt.Sprintf("delete all %T", obj))
	return c.Client.DeleteAllOf(ctx, obj, opts...)
}

func (c *quorumMutationClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.PodList); ok && c.failPodList {
		return fmt.Errorf("pod discovery unavailable")
	}
	return c.Client.List(ctx, list, opts...)
}

type quorumRoundTripper func(*http.Request) (*http.Response, error)

func (f quorumRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type quorumFixture struct {
	r              *TypesenseClusterReconciler
	client         *quorumMutationClient
	ts             *tsv1alpha1.TypesenseCluster
	sts            *appsv1.StatefulSet
	cm             *corev1.ConfigMap
	secret         *corev1.Secret
	slice          *discoveryv1.EndpointSlice
	states         []NodeState
	timeoutLeader  bool
	statusCode     int
	health         NodeHealth
	logs           string
	healthRequests int
}

func newQuorumFixture(t *testing.T, states ...NodeState) *quorumFixture {
	t.Helper()
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	g.Expect(discoveryv1.AddToScheme(scheme)).To(Succeed())
	g.Expect(tsv1alpha1.AddToScheme(scheme)).To(Succeed())
	f := &quorumFixture{
		states:     states,
		statusCode: http.StatusOK,
		health:     NodeHealth{Ok: true},
		ts: &tsv1alpha1.TypesenseCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "quorum", Namespace: "default", UID: "cluster-uid"},
			Spec: tsv1alpha1.TypesenseClusterSpec{
				Replicas: int32(len(states)), Image: "typesense/typesense:30.0",
				ApiPort: 8108, PeeringPort: 8107, HealthProbeTimeoutInMilliseconds: 500,
				ResetPeersOnError: true, ForceResetPeersConfigOnUpdate: true,
				Storage: &tsv1alpha1.StorageSpec{StorageClassName: "standard", Size: resource.MustParse("1Gi")},
			},
		},
		secret: &corev1.Secret{Data: map[string][]byte{ClusterAdminApiKeySecretKeyName: []byte("test-key")}},
	}
	f.ts.Status.Conditions = []metav1.Condition{{Type: ConditionTypeReady, Status: metav1.ConditionTrue, Reason: string(ConditionReasonQuorumReady)}}
	f.client = &quorumMutationClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&corev1.Pod{}, &appsv1.StatefulSet{}, &tsv1alpha1.TypesenseCluster{}).Build()}
	f.r = &TypesenseClusterReconciler{Client: f.client, Scheme: scheme, logger: logr.Discard(), Recorder: record.NewFakeRecorder(100)}
	f.r.Configuration = &rest.Config{Host: "http://quorum.test", Transport: quorumRoundTripper(func(req *http.Request) (*http.Response, error) {
		body := ""
		code := http.StatusOK
		if req.URL.Path == "/version" {
			body = `{"gitVersion":"v1.32.0"}`
		} else if req.URL.Path == "/api" {
			body = `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`
		} else if req.URL.Path == "/apis" {
			body = `{"kind":"APIGroupList","apiVersion":"v1","groups":[]}`
		} else if strings.HasSuffix(req.URL.Path, "/log") {
			body = f.logs
		} else {
			index := -1
			for i := range f.states {
				if strings.Contains(req.URL.Path, fmt.Sprintf("quorum-sts-%d:", i)) {
					index = i
					break
				}
			}
			if index < 0 {
				return nil, fmt.Errorf("unexpected test request: %s", req.URL.Path)
			}
			var payload interface{}
			switch {
			case strings.HasSuffix(req.URL.Path, "/status"):
				if index == 0 && f.timeoutLeader {
					return nil, context.DeadlineExceeded
				}
				code = f.statusCode
				payload = NodeStatus{State: f.states[index]}
			case strings.HasSuffix(req.URL.Path, "/health"):
				f.healthRequests++
				payload = f.health
			default:
				return nil, fmt.Errorf("unexpected test request: %s", req.URL.Path)
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			body = string(data)
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	var err error
	f.r.ClientSet, err = kubernetes.NewForConfig(f.r.Configuration)
	g.Expect(err).NotTo(HaveOccurred())
	key := client.ObjectKey{Namespace: f.ts.Namespace, Name: fmt.Sprintf(ClusterStatefulSet, f.ts.Name)}
	f.sts, err = f.r.buildStatefulSet(context.Background(), key, f.ts)
	g.Expect(err).NotTo(HaveOccurred())
	f.sts.Status.Replicas = f.ts.Spec.Replicas
	f.sts.Status.ReadyReplicas = f.ts.Spec.Replicas
	f.slice = &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "quorum-peers", Namespace: f.ts.Namespace, Labels: map[string]string{discoveryv1.LabelServiceName: f.sts.Spec.ServiceName}},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	nodes := make([]string, 0, len(states))
	fallback := make([]string, 0, len(states))
	objects := []client.Object{f.ts, f.sts}
	for i := range states {
		name := fmt.Sprintf("quorum-sts-%d", i)
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		nodes = append(nodes, fmt.Sprintf("%s:8107:8108", ip))
		fallback = append(fallback, fmt.Sprintf("%s.quorum-sts-svc:8107:8108", name))
		f.slice.Endpoints = append(f.slice.Endpoints, discoveryv1.Endpoint{Addresses: []string{ip}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: name}})
		objects = append(objects,
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: f.ts.Namespace, UID: types.UID(name), Labels: getLabels(f.ts)}, Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip}},
			&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data-" + name, Namespace: f.ts.Namespace, UID: types.UID("data-" + name)}},
		)
	}
	f.cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf(ClusterNodesConfigMap, f.ts.Name), Namespace: f.ts.Namespace}, Data: map[string]string{"nodes": strings.Join(nodes, ","), "fallback": strings.Join(fallback, ",")}}
	objects = append(objects, f.cm, f.slice)
	for _, obj := range objects {
		g.Expect(f.client.Client.Create(context.Background(), obj)).To(Succeed())
	}
	return f
}

func (f *quorumFixture) expectPreserved(t *testing.T) {
	t.Helper()
	g := NewWithT(t)
	g.Expect(f.client.mutations).To(BeEmpty(), "health observations must not mutate workload, membership, or storage")
	sts := &appsv1.StatefulSet{}
	g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.sts), sts)).To(Succeed())
	g.Expect(sts.Spec.Replicas).To(Equal(f.sts.Spec.Replicas))
	cm := &corev1.ConfigMap{}
	g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.cm), cm)).To(Succeed())
	g.Expect(cm.Data).To(Equal(f.cm.Data))
	for i := range f.states {
		name := fmt.Sprintf("quorum-sts-%d", i)
		pod := &corev1.Pod{}
		g.Expect(f.client.Get(context.Background(), client.ObjectKey{Namespace: f.ts.Namespace, Name: name}, pod)).To(Succeed())
		g.Expect(string(pod.UID)).To(Equal(name))
		pvc := &corev1.PersistentVolumeClaim{}
		g.Expect(f.client.Get(context.Background(), client.ObjectKey{Namespace: f.ts.Namespace, Name: "data-" + name}, pvc)).To(Succeed())
		g.Expect(string(pvc.UID)).To(Equal("data-" + name))
	}
}

func TestQuorumSafety(t *testing.T) {
	cases := []struct {
		name       string
		states     []NodeState
		timeout    bool
		unhealthy  bool
		logs       string
		statusCode int
		want       ConditionQuorum
		wantError  bool
	}{
		{name: "leader timeout with all health probes successful", states: []NodeState{LeaderState, FollowerState, FollowerState}, timeout: true, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "leaderless followers", states: []NodeState{FollowerState, FollowerState, FollowerState}, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "election in progress", states: []NodeState{CandidateState, FollowerState, CandidateState}, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "split brain", states: []NodeState{LeaderState, FollowerState, LeaderState}, want: ConditionReasonQuorumNotReady, wantError: true},
		{name: "unknown and error states are not voters", states: []NodeState{LeaderState, ErrorState, NodeState("UNKNOWN")}, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "failed status HTTP cannot elect a leader", states: []NodeState{LeaderState, FollowerState, FollowerState}, statusCode: http.StatusServiceUnavailable, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "health failure", states: []NodeState{LeaderState, FollowerState, FollowerState}, unhealthy: true, want: ConditionReasonQuorumNotReady},
		{name: "singleton error does not purge", states: []NodeState{ErrorState}, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "singleton timeout does not purge", states: []NodeState{LeaderState}, timeout: true, want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "fatal raft log does not purge", states: []NodeState{LeaderState}, logs: "Can't truncate logs before _applied_id", want: ConditionReasonQuorumNotReadyWaitATerm},
		{name: "healthy three nodes", states: []NodeState{LeaderState, FollowerState, FollowerState}, want: ConditionReasonQuorumReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			f := newQuorumFixture(t, tc.states...)
			f.timeoutLeader, f.logs = tc.timeout, tc.logs
			f.health.Ok = !tc.unhealthy
			if tc.statusCode != 0 {
				f.statusCode = tc.statusCode
			}
			// Repeat the real membership/quorum phases to catch delayed recovery.
			for range 2 {
				updated, err := f.r.ReconcileConfigMap(context.Background(), *f.ts)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(updated).To(Equal(ptr.To(false)))
				_, _, err = f.r.ReconcileStatefulSet(context.Background(), f.ts)
				g.Expect(err).NotTo(HaveOccurred())
				condition, size, err := f.r.ReconcileQuorum(context.Background(), f.ts, f.secret, client.ObjectKeyFromObject(f.sts))
				f.expectPreserved(t)
				g.Expect(err != nil).To(Equal(tc.wantError))
				g.Expect(condition).To(Equal(tc.want))
				g.Expect(size).To(BeZero())
			}
			if tc.timeout && len(tc.states) == 3 {
				g.Expect(f.healthRequests).To(Equal(6), "the leader timeout must be followed by successful health checks on all three nodes")
			}
		})
	}
}

func TestQuorumUsesConfiguredReplicaCount(t *testing.T) {
	g := NewWithT(t)
	f := newQuorumFixture(t, LeaderState)
	f.ts.Spec.Replicas = 3
	f.sts.Spec.Replicas = ptr.To[int32](3)
	// Status and discovered peers still report one; this is not a safe quorum.
	g.Expect(f.client.Client.Update(context.Background(), f.sts)).To(Succeed())
	condition, _, err := f.r.ReconcileQuorum(context.Background(), f.ts, f.secret, client.ObjectKeyFromObject(f.sts))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(condition).To(Equal(ConditionQuorum(ConditionReasonStatefulSetNotReady)))
	f.expectPreserved(t)
}

func TestQuorumResourceErrorsRequireAttention(t *testing.T) {
	g := NewWithT(t)
	f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
	f.health = NodeHealth{Ok: false, ResourceError: ptr.To(OutOfDisk)}
	condition, _, err := f.r.ReconcileQuorum(context.Background(), f.ts, f.secret, client.ObjectKeyFromObject(f.sts))
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(condition).To(Equal(ConditionReasonQuorumNeedsAttentionMemoryOrDiskIssue))
	f.expectPreserved(t)
}

func TestPeerDiscoveryPreservesMembership(t *testing.T) {
	for _, scenario := range []string{"partial endpoints", "duplicate endpoints", "stale endpoints", "pod list failure"} {
		t.Run(scenario, func(t *testing.T) {
			g := NewWithT(t)
			f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
			switch scenario {
			case "partial endpoints":
				f.slice.Endpoints = f.slice.Endpoints[:1]
			case "duplicate endpoints":
				f.slice.Endpoints = []discoveryv1.Endpoint{f.slice.Endpoints[0], f.slice.Endpoints[0], f.slice.Endpoints[0]}
			case "stale endpoints":
				f.slice.Endpoints[1].Addresses = []string{"10.0.1.2"}
				f.slice.Endpoints[2].Addresses = []string{"10.0.1.3"}
			case "pod list failure":
				f.client.failPodList = true
			}
			g.Expect(f.client.Client.Update(context.Background(), f.slice)).To(Succeed())
			updated, err := f.r.ReconcileConfigMap(context.Background(), *f.ts)
			f.expectPreserved(t)
			g.Expect(updated).To(Equal(ptr.To(false)))
			g.Expect(err != nil).To(Equal(scenario == "pod list failure"))
		})
	}
}

func TestPeerDiscoveryUpdatesReplacementAddress(t *testing.T) {
	g := NewWithT(t)
	f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
	pod := &corev1.Pod{}
	g.Expect(f.client.Get(context.Background(), client.ObjectKey{Namespace: f.ts.Namespace, Name: "quorum-sts-1"}, pod)).To(Succeed())
	pod.Status.PodIP = "10.0.0.4"
	g.Expect(f.client.Status().Update(context.Background(), pod)).To(Succeed())
	f.slice.Endpoints[1].Addresses = []string{pod.Status.PodIP}
	g.Expect(f.client.Client.Update(context.Background(), f.slice)).To(Succeed())
	updated, err := f.r.ReconcileConfigMap(context.Background(), *f.ts)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updated).To(Equal(ptr.To(true)))
	cm := &corev1.ConfigMap{}
	g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.cm), cm)).To(Succeed())
	g.Expect(cm.Data["nodes"]).To(Equal("10.0.0.1:8107:8108,10.0.0.3:8107:8108,10.0.0.4:8107:8108"))
	g.Expect(cm.Data["fallback"]).To(Equal(f.cm.Data["fallback"]))
	sts := &appsv1.StatefulSet{}
	g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.sts), sts)).To(Succeed())
	g.Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
}

func TestInitialDeploymentKeepsAllPeers(t *testing.T) {
	for _, replicas := range []int{1, 3} {
		t.Run(fmt.Sprintf("replicas=%d", replicas), func(t *testing.T) {
			g := NewWithT(t)
			states := make([]NodeState, replicas)
			f := newQuorumFixture(t, states...)
			g.Expect(f.client.Client.Delete(context.Background(), f.sts)).To(Succeed())
			g.Expect(f.client.Client.Delete(context.Background(), f.cm)).To(Succeed())
			updated, err := f.r.ReconcileConfigMap(context.Background(), *f.ts)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updated).To(BeNil())
			cm := &corev1.ConfigMap{}
			g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.cm), cm)).To(Succeed())
			g.Expect(cm.Data["nodes"]).To(Equal(f.cm.Data["fallback"]))
			// A retry after creating the ConfigMap must not delete it just because
			// the initial StatefulSet creation has not succeeded yet.
			f.client.mutations = nil
			updated, err = f.r.ReconcileConfigMap(context.Background(), *f.ts)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(updated).To(Equal(ptr.To(false)))
			g.Expect(f.client.mutations).To(BeEmpty())
			sts, _, err := f.r.ReconcileStatefulSet(context.Background(), f.ts)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(*sts.Spec.Replicas).To(Equal(int32(replicas)))
			g.Expect(sts.Spec.PodManagementPolicy).To(Equal(appsv1.ParallelPodManagement))
		})
	}
}

func TestStatefulSetStorageAccessModeCompatibility(t *testing.T) {
	for _, mode := range []string{"", "ReadWriteOnce", "ReadWriteMany"} {
		t.Run("accessMode="+mode, func(t *testing.T) {
			g := NewWithT(t)
			f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
			f.ts.Spec.Storage.AccessMode = mode
			sts, err := f.r.buildStatefulSet(context.Background(), client.ObjectKeyFromObject(f.sts), f.ts)
			g.Expect(err).NotTo(HaveOccurred())
			want := mode
			if want == "" {
				want = "ReadWriteOnce"
			}
			claim := sts.Spec.VolumeClaimTemplates[0]
			g.Expect(claim.Spec.AccessModes).To(Equal([]corev1.PersistentVolumeAccessMode{corev1.PersistentVolumeAccessMode(want)}))
			g.Expect(claim.Spec.StorageClassName).To(Equal(ptr.To("standard")))
			g.Expect(claim.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("1Gi")))
			g.Expect(f.ts.Spec.Storage.AccessMode).To(Equal(mode), "defaulting must not mutate the CR")
		})
	}
}

func TestQuorumGrowthNeverUsesLaggingReplicaStatus(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		t.Run(fmt.Sprintf("healthy=%t", healthy), func(t *testing.T) {
			g := NewWithT(t)
			f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
			f.ts.Spec.Replicas = 5
			f.ts.Spec.IncrementalQuorumRecovery = true
			f.health.Ok = healthy
			f.sts.Status.Replicas = 0
			g.Expect(f.client.Status().Update(context.Background(), f.sts)).To(Succeed())
			condition, _, err := f.r.ReconcileQuorum(context.Background(), f.ts, f.secret, client.ObjectKeyFromObject(f.sts))
			if !healthy {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(condition).To(Equal(ConditionReasonQuorumNotReady))
				f.expectPreserved(t)
				return
			}
			// The fourth Pod is not yet discoverable. Scaling can grow, but the
			// existing three-peer nodes file must remain intact until it is.
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(condition).To(Equal(ConditionReasonQuorumUpgraded))
			sts := &appsv1.StatefulSet{}
			g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.sts), sts)).To(Succeed())
			g.Expect(*sts.Spec.Replicas).To(Equal(int32(4)))
			cm := &corev1.ConfigMap{}
			g.Expect(f.client.Get(context.Background(), client.ObjectKeyFromObject(f.cm), cm)).To(Succeed())
			g.Expect(cm.Data).To(Equal(f.cm.Data))
			g.Expect(f.client.mutations).To(HaveLen(1), "only the requested growth may mutate resources")
		})
	}
}

func TestReconcileRepairsInfrastructureWithIncompletePeerDiscovery(t *testing.T) {
	for _, scenario := range []string{"unschedulable pod resource repair", "missing headless service"} {
		t.Run(scenario, func(t *testing.T) {
			g := NewWithT(t)
			ctx := context.Background()
			f := newQuorumFixture(t, LeaderState, FollowerState, FollowerState)
			g.Expect(batchv1.AddToScheme(f.r.Scheme)).To(Succeed())
			g.Expect(networkingv1.AddToScheme(f.r.Scheme)).To(Succeed())
			var err error
			f.r.DiscoveryClient, err = discovery.NewDiscoveryClientForConfig(f.r.Configuration)
			g.Expect(err).NotTo(HaveOccurred())
			f.secret.Name = fmt.Sprintf(ClusterAdminApiKeySecret, f.ts.Name)
			f.secret.Namespace = f.ts.Namespace
			g.Expect(f.client.Client.Create(ctx, f.secret)).To(Succeed())
			g.Expect(f.r.ReconcileServices(ctx, *f.ts)).To(Succeed())

			survivingPods := len(f.states)
			if scenario == "unschedulable pod resource repair" {
				pod := &corev1.Pod{}
				g.Expect(f.client.Get(ctx, client.ObjectKey{Namespace: f.ts.Namespace, Name: "quorum-sts-2"}, pod)).To(Succeed())
				pod.Status = corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{{
						Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
					}},
				}
				g.Expect(f.client.Status().Update(ctx, pod)).To(Succeed())
				f.slice.Endpoints = f.slice.Endpoints[:2]
				g.Expect(f.client.Client.Update(ctx, f.slice)).To(Succeed())
				resources := f.ts.Spec.GetResources()
				resources.Requests[corev1.ResourceCPU] = resource.MustParse("50m")
				f.ts.Spec.Resources = &resources
				g.Expect(f.client.Client.Update(ctx, f.ts)).To(Succeed())
				f.ts.Status.Conditions[0].Status = metav1.ConditionFalse
				f.ts.Status.Conditions[0].Reason = string(ConditionReasonQuorumNotReady)
				g.Expect(f.client.Status().Update(ctx, f.ts)).To(Succeed())
				// Normal unschedulable-Pod recreation is allowed, but running
				// peers and all PVCs must survive this workload repair.
				survivingPods = 2
			} else {
				headless := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
					Namespace: f.ts.Namespace, Name: f.sts.Spec.ServiceName,
				}}
				g.Expect(f.client.Client.Delete(ctx, headless)).To(Succeed())
				g.Expect(f.client.Client.Delete(ctx, f.slice)).To(Succeed())
			}

			f.client.mutations = nil
			result, err := f.r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(f.ts)})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			sts := &appsv1.StatefulSet{}
			g.Expect(f.client.Get(ctx, client.ObjectKeyFromObject(f.sts), sts)).To(Succeed())
			g.Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
			cm := &corev1.ConfigMap{}
			g.Expect(f.client.Get(ctx, client.ObjectKeyFromObject(f.cm), cm)).To(Succeed())
			g.Expect(cm.Data).To(Equal(f.cm.Data))
			if scenario == "unschedulable pod resource repair" {
				g.Expect(sts.Spec.Template.Spec.Containers[0].Resources).To(Equal(*f.ts.Spec.Resources))
			} else {
				headless := &corev1.Service{}
				g.Expect(f.client.Get(ctx, client.ObjectKey{Namespace: f.ts.Namespace, Name: f.sts.Spec.ServiceName}, headless)).To(Succeed())
				g.Expect(headless.Spec.ClusterIP).To(Equal(corev1.ClusterIPNone))
				g.Expect(headless.Spec.Selector).To(Equal(getLabels(f.ts)))
				g.Expect(headless.Spec.PublishNotReadyAddresses).To(BeTrue())
			}
			for i := range survivingPods {
				name := fmt.Sprintf("quorum-sts-%d", i)
				pod := &corev1.Pod{}
				g.Expect(f.client.Get(ctx, client.ObjectKey{Namespace: f.ts.Namespace, Name: name}, pod)).To(Succeed())
				g.Expect(string(pod.UID)).To(Equal(name))
			}
			for i := range f.states {
				name := fmt.Sprintf("data-quorum-sts-%d", i)
				pvc := &corev1.PersistentVolumeClaim{}
				g.Expect(f.client.Get(ctx, client.ObjectKey{Namespace: f.ts.Namespace, Name: name}, pvc)).To(Succeed())
				g.Expect(string(pvc.UID)).To(Equal(name))
			}
		})
	}
}
