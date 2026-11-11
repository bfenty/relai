package vrouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/proxy"
)

func testManager() *Manager {
	return NewManager(nil, nil, nil)
}

func testStates(n int) []*memberState {
	states := make([]*memberState, n)
	for i := range states {
		states[i] = &memberState{health: "active", slot: hostlimit.NewSlot(1)}
	}
	return states
}

func testMembers(n int) []db.RouterMember {
	members := make([]db.RouterMember, n)
	for i := range members {
		members[i] = db.RouterMember{ModelMapID: int64(i + 1)}
	}
	return members
}

func TestNormalizeStrategyAcceptsDocumentedAndLegacyNames(t *testing.T) {
	cases := map[string]string{
		"":                   "sticky",
		"sticky":             "sticky",
		"round_robin":        "round_robin",
		"round-robin":        "round_robin",
		"least_connections":  "least_connections",
		"least-connections":  "least_connections",
		"least-busy":         "least_connections",
		"cache-aware-sticky": "cache_aware_sticky",
		"cache_aware_sticky": "cache_aware_sticky",
		"adaptive-thinking":  "adaptive_thinking",
		"adaptive_thinking":  "adaptive_thinking",
	}

	for input, want := range cases {
		if got := normalizeStrategy(input); got != want {
			t.Fatalf("normalizeStrategy(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCacheAwareStickyRestoresAffinityAfterManagerRestart(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	backend, err := d.Backends.Create(db.BackendInput{Name: "b", BaseURL: "http://backend"})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	firstMap, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: backend.ID, LocalModel: "local-a", RemoteModel: "remote-a"})
	if err != nil {
		t.Fatalf("create first map: %v", err)
	}
	secondMap, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: backend.ID, LocalModel: "local-b", RemoteModel: "remote-b"})
	if err != nil {
		t.Fatalf("create second map: %v", err)
	}
	router, err := d.Routers.Create(db.RouterInput{Name: "cache-test", Strategy: "cache_aware_sticky"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	members := []db.RouterMember{{ModelMapID: firstMap.ID}, {ModelMapID: secondMap.ID}}
	first := NewManager(d, nil, nil)
	first.setPersistentSession(router.ID, "agent-a", members[1].ModelMapID)

	second := NewManager(d, nil, nil)
	states := testStates(2)
	idx, err := second.acquireMember(context.Background(), router, states, members, "agent-a", nil, 0, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()
	if idx != 1 {
		t.Fatalf("restored affinity selected member %d, want 1", idx)
	}
}

func TestCacheAwareStickyPrefersMemberWithKVPoolHeadroom(t *testing.T) {
	m := testManager()
	states := testStates(2)
	members := testMembers(2)
	m.setCacheStatus(members[0].ModelMapID, cacheStatus{available: true, poolUsageRatio: 0.92, tokenHitRate: 0.10})
	m.setCacheStatus(members[1].ModelMapID, cacheStatus{available: true, poolUsageRatio: 0.20, tokenHitRate: 0.10})

	idx, err := m.acquireMember(context.Background(), &db.Router{ID: 1, Strategy: "cache_aware_sticky"}, states, members, "", nil, 0, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()
	if idx != 1 {
		t.Fatalf("cache-aware first placement selected member %d, want 1 with headroom", idx)
	}
}

func TestRefreshCacheStatusReadsHalogenTelemetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cache":
			_, _ = w.Write([]byte(`{"token_hit_rate":0.76,"pool":{"usage_ratio":0.42}}`))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok","queued":3,"longest_wait_s":12.5}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := testManager()
	m.refreshCacheStatus(&db.ModelMap{ID: 12, BaseURL: server.URL})
	status, ok := m.cacheStatusFor(12)
	if !ok || status.tokenHitRate != 0.76 || status.poolUsageRatio != 0.42 {
		t.Fatalf("cache status = %#v, ok=%v", status, ok)
	}
	if !status.engineQueueSeen || status.engineQueued != 3 || status.engineWaitSecs != 12.5 {
		t.Fatalf("engine queue telemetry = seen=%v queued=%d wait=%v, want seen 3 12.5",
			status.engineQueueSeen, status.engineQueued, status.engineWaitSecs)
	}
}

func TestRefreshCacheStatusWithoutHealthQueue(t *testing.T) {
	// A backend whose /health lacks queue fields (old Halogen, plain
	// llama.cpp) must leave EngineQueueSeen=false rather than claim zero.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cache":
			_, _ = w.Write([]byte(`{"token_hit_rate":0.5,"pool":{"usage_ratio":0.1}}`))
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := testManager()
	m.refreshCacheStatus(&db.ModelMap{ID: 13, BaseURL: server.URL})
	status, ok := m.cacheStatusFor(13)
	if !ok {
		t.Fatal("cache status missing")
	}
	if status.engineQueueSeen {
		t.Fatalf("engine queue seen without fields: %#v", status)
	}
}

func TestLeastConnectionsUsesGlobalMemberLoad(t *testing.T) {
	m := testManager()
	states := testStates(2)
	states[0].slot.TryAcquire()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	idx, err := m.acquireMember(ctx, &db.Router{ID: 1, Strategy: "least_connections"}, states, testMembers(2), "", nil, 0, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()

	if idx != 1 {
		t.Fatalf("expected idle member 1, got %d", idx)
	}
}

func TestStickyFirstPlacementDistributesDistinctSessions(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 42, Strategy: "sticky"}
	members := testMembers(2)
	counts := map[int]int{}

	for _, key := range []string{"agent-a", "agent-b", "agent-c", "agent-d", "agent-e", "agent-f", "agent-g", "agent-h"} {
		states := testStates(2)
		idx, err := m.acquireMember(context.Background(), router, states, members, key, nil, 0, nil)
		if err != nil {
			t.Fatalf("acquireMember failed: %v", err)
		}
		counts[idx]++
		states[idx].release()
	}

	if len(counts) < 2 {
		t.Fatalf("expected distinct sessions to distribute across members, got counts %v", counts)
	}
}

func TestStickyKeepsExistingSessionOnAssignedMember(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 7, Strategy: "sticky"}
	states := testStates(2)
	members := testMembers(2)
	m.setSession("agent-a", members[1].ModelMapID)

	idx, err := m.acquireMember(context.Background(), router, states, members, "agent-a", nil, 0, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()

	if idx != 1 {
		t.Fatalf("expected sticky session on member 1, got %d", idx)
	}
}

func TestStickySessionWaitsForAssignedBusyMember(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 7, Strategy: "sticky"}
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire()
	m.setSession("agent-a", members[0].ModelMapID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := m.acquireMember(ctx, router, states, members, "agent-a", nil, 0, nil)
	if err == nil {
		t.Fatal("expected sticky session to wait for its assigned busy member")
	}
}

func TestStickyDifferentSessionUsesIdleMemberWhenAnotherSessionIsBusy(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 7, Strategy: "sticky"}
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire()
	m.setSession("agent-a", members[0].ModelMapID)

	idx, err := m.acquireMember(context.Background(), router, states, members, "agent-b", nil, 0, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()

	if idx != 1 {
		t.Fatalf("expected different session to use idle member 1, got %d", idx)
	}
}

func TestQueuedAcquireCleansUpWaitersOnCancellation(t *testing.T) {
	m := testManager()
	states := testStates(2)
	states[0].slot.TryAcquire()
	states[1].slot.TryAcquire()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := m.acquireMember(ctx, &db.Router{ID: 1, Strategy: "least_connections"}, states, testMembers(2), "", nil, 0, nil)
	if err == nil {
		t.Fatal("expected timeout when all members stay busy")
	}

	for i, state := range states {
		_, waiters, _ := state.slot.Load()
		if waiters != 0 {
			t.Fatalf("member %d has %d waiters after cancellation", i, waiters)
		}
	}
}

func TestAcquireMemberFailsFastWhenNoHealthyMembersRemain(t *testing.T) {
	m := testManager()
	states := testStates(2)
	states[0].setHealth("offline")
	states[1].setHealth("offline")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()

	_, err := m.acquireMember(ctx, &db.Router{ID: 1, Strategy: "least_connections"}, states, testMembers(2), "", nil, 0, nil)
	if err == nil {
		t.Fatal("expected error when all members are offline")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("expected fast failure for offline members, took %s", time.Since(start))
	}
}

func TestFairAdmissionQueuesSecondRequestFromSameClient(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 1, Name: "pool"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &proxy.RequestContext{ClientKey: "api_key:1", RequestedModel: "pool"}

	releaseFirst, queued, err := m.acquireAdmission(ctx, router, client, 1)
	if err != nil {
		t.Fatalf("first acquireAdmission failed: %v", err)
	}
	if queued {
		t.Fatal("first request should not queue")
	}

	acquiredSecond := make(chan bool, 1)
	go func() {
		releaseSecond, queuedSecond, err := m.acquireAdmission(ctx, router, client, 1)
		if err != nil {
			t.Errorf("second acquireAdmission failed: %v", err)
			acquiredSecond <- false
			return
		}
		defer releaseSecond()
		acquiredSecond <- queuedSecond
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second request from same client should wait while first is active")
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	select {
	case queuedSecond := <-acquiredSecond:
		if !queuedSecond {
			t.Fatal("second request should report that it queued")
		}
	case <-time.After(time.Second):
		t.Fatal("second request did not leave admission queue after first released")
	}
}

func TestFairAdmissionDoesNotLetOneClientBlockAnotherClient(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 1, Name: "pool"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	releaseA1, _, err := m.acquireAdmission(ctx, router, &proxy.RequestContext{ClientKey: "api_key:1"}, 2)
	if err != nil {
		t.Fatalf("client A first acquire failed: %v", err)
	}
	releaseA2, _, err := m.acquireAdmission(ctx, router, &proxy.RequestContext{ClientKey: "api_key:1"}, 2)
	if err != nil {
		t.Fatalf("client A second acquire failed: %v", err)
	}
	defer releaseA2()

	aQueued := make(chan struct{}, 1)
	go func() {
		releaseA3, _, err := m.acquireAdmission(ctx, router, &proxy.RequestContext{ClientKey: "api_key:1"}, 2)
		if err != nil {
			t.Errorf("client A third acquire failed: %v", err)
			return
		}
		defer releaseA3()
		aQueued <- struct{}{}
	}()
	time.Sleep(20 * time.Millisecond)

	bQueued := make(chan bool, 1)
	releaseBNow := make(chan struct{})
	go func() {
		releaseB, queuedB, err := m.acquireAdmission(ctx, router, &proxy.RequestContext{ClientKey: "api_key:2"}, 2)
		if err != nil {
			t.Errorf("client B acquire failed: %v", err)
			bQueued <- false
			return
		}
		bQueued <- queuedB
		<-releaseBNow
		releaseB()
	}()
	time.Sleep(20 * time.Millisecond)

	select {
	case <-aQueued:
		t.Fatal("client A third request should still be queued")
	case <-bQueued:
		t.Fatal("client B request should wait until router capacity frees")
	default:
	}

	releaseA1()
	select {
	case queuedB := <-bQueued:
		if !queuedB {
			t.Fatal("client B should report that it queued")
		}
	case <-time.After(time.Second):
		t.Fatal("client B request did not release after capacity freed")
	}
	select {
	case <-aQueued:
		t.Fatal("client A third request should remain queued while client B uses the freed slot")
	default:
	}
	close(releaseBNow)
}

func TestFairAdmissionUsesRouterCapacityForSameClient(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 1, Name: "pool"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &proxy.RequestContext{ClientKey: "api_key:1", RequestedModel: "pool"}

	releaseFirst, queued, err := m.acquireAdmission(ctx, router, client, 2)
	if err != nil {
		t.Fatalf("first acquireAdmission failed: %v", err)
	}
	defer releaseFirst()
	if queued {
		t.Fatal("first request should not queue")
	}

	releaseSecond, queued, err := m.acquireAdmission(ctx, router, client, 2)
	if err != nil {
		t.Fatalf("second acquireAdmission failed: %v", err)
	}
	defer releaseSecond()
	if queued {
		t.Fatal("second request should not queue when router has two healthy members")
	}

	acquiredThird := make(chan bool, 1)
	go func() {
		releaseThird, queuedThird, err := m.acquireAdmission(ctx, router, client, 2)
		if err != nil {
			t.Errorf("third acquireAdmission failed: %v", err)
			acquiredThird <- false
			return
		}
		defer releaseThird()
		acquiredThird <- queuedThird
	}()

	select {
	case <-acquiredThird:
		t.Fatal("third request from same client should wait when router capacity is full")
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	select {
	case queuedThird := <-acquiredThird:
		if !queuedThird {
			t.Fatal("third request should report that it queued")
		}
	case <-time.After(time.Second):
		t.Fatal("third request did not leave admission queue after capacity freed")
	}
}

func TestSelectRoutableMemberSkipsInactiveBackendBeforeProxying(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	inactiveBackend, err := d.Backends.Create(db.BackendInput{Name: "cold", BaseURL: "http://cold", Status: "inactive"})
	if err != nil {
		t.Fatalf("create inactive backend: %v", err)
	}
	activeBackend, err := d.Backends.Create(db.BackendInput{Name: "ready", BaseURL: "http://ready", Status: "active"})
	if err != nil {
		t.Fatalf("create active backend: %v", err)
	}
	inactiveMap, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: inactiveBackend.ID, LocalModel: "pool-cold", RemoteModel: "remote-a"})
	if err != nil {
		t.Fatalf("create inactive map: %v", err)
	}
	activeMap, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: activeBackend.ID, LocalModel: "pool-ready", RemoteModel: "remote-b"})
	if err != nil {
		t.Fatalf("create active map: %v", err)
	}
	router, err := d.Routers.Create(db.RouterInput{Name: "pool", Strategy: "round_robin"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	firstMember, err := d.Routers.AddMember(router.ID, inactiveMap.ID, 10)
	if err != nil {
		t.Fatalf("add inactive member: %v", err)
	}
	secondMember, err := d.Routers.AddMember(router.ID, activeMap.ID, 1)
	if err != nil {
		t.Fatalf("add active member: %v", err)
	}

	m := NewManager(d, nil, nil)
	states := m.getOrCreateStates([]db.RouterMember{*firstMember, *secondMember})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	sel, err := m.selectRoutableMember(ctx, router, states, []db.RouterMember{*firstMember, *secondMember}, "", 0, nil)
	if err != nil {
		t.Fatalf("selectRoutableMember failed: %v", err)
	}
	defer sel.state.release()

	if sel.backend.ID != activeBackend.ID {
		t.Fatalf("selected backend %d, want active backend %d", sel.backend.ID, activeBackend.ID)
	}
	if states[0].getHealth() != "offline" {
		t.Fatalf("expected skipped inactive member to be marked offline, got %q", states[0].getHealth())
	}
}

func TestSelectRoutableMemberLogsSelectionEvent(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	backend, err := d.Backends.Create(db.BackendInput{Name: "ready", BaseURL: "http://ready", Status: "active"})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	modelMap, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: backend.ID, LocalModel: "pool", RemoteModel: "remote"})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}
	router, err := d.Routers.Create(db.RouterInput{Name: "pool", Strategy: "least_connections"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	member, err := d.Routers.AddMember(router.ID, modelMap.ID, 1)
	if err != nil {
		t.Fatalf("add member: %v", err)
	}

	m := NewManager(d, nil, nil)
	sel := &selectedMember{
		index:    0,
		state:    m.getOrCreateState(modelMap.ID),
		member:   *member,
		modelMap: modelMap,
		backend:  backend,
	}
	m.logSelection(router, sel, true)

	events, total, err := d.SystemEvents.List(db.EventFilters{EventType: db.EventProxy, Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if total != 1 {
		t.Fatalf("event total = %d, want 1", total)
	}
	if events[0].Message != "virtual router selected member" {
		t.Fatalf("event message = %q", events[0].Message)
	}
	if events[0].Metadata["backend_name"] != "ready" {
		t.Fatalf("backend_name metadata = %#v", events[0].Metadata["backend_name"])
	}
	if events[0].Metadata["session_key_present"] != true {
		t.Fatalf("session_key_present metadata = %#v", events[0].Metadata["session_key_present"])
	}
}

func TestComputeSessionKeyUsesStableEarlyConversationShape(t *testing.T) {
	req := &http.Request{Header: make(http.Header)}
	first := []byte(`{"messages":[{"role":"system","content":"base"},{"role":"user","content":"task"},{"role":"assistant","content":"answer"}]}`)
	later := []byte(`{"messages":[{"role":"system","content":"base"},{"role":"user","content":"task"},{"role":"assistant","content":"answer"},{"role":"user","content":"more context"}]}`)

	firstKey := computeSessionKey(req, first)
	laterKey := computeSessionKey(req, later)
	if firstKey == "" {
		t.Fatal("expected non-empty session key")
	}
	if firstKey != laterKey {
		t.Fatalf("expected later prompt growth to keep the same key, got %q and %q", firstKey, laterKey)
	}

	other := computeSessionKey(req, []byte(`{"messages":[{"role":"system","content":"base"},{"role":"user","content":"other task"}]}`))
	if firstKey == other {
		t.Fatal("expected different first user task to produce a different key")
	}
	if !strings.HasPrefix(firstKey, "s:") {
		t.Fatalf("expected hashed session prefix, got %q", firstKey)
	}
}

func TestComputeSessionKeyHandlesStructuredMessageContent(t *testing.T) {
	req := &http.Request{Header: make(http.Header)}
	first := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"build the thing"}]}]}`)
	later := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"build the thing"}]},{"role":"assistant","content":"ok"},{"role":"user","content":[{"type":"text","text":"more"}]}]}`)
	other := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"fix the other thing"}]}]}`)

	firstKey := computeSessionKey(req, first)
	laterKey := computeSessionKey(req, later)
	otherKey := computeSessionKey(req, other)

	if firstKey == "" {
		t.Fatal("expected structured content to produce a session key")
	}
	if firstKey != laterKey {
		t.Fatalf("expected structured prompt growth to keep same key, got %q and %q", firstKey, laterKey)
	}
	if firstKey == otherKey {
		t.Fatal("expected different structured prompts to produce different keys")
	}
}

func TestMembersOnSameHostShareConcurrencySlot(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	host, err := d.Backends.Create(db.BackendInput{Name: "gpu-1", BaseURL: "http://gpu-1", MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	mapA, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: host.ID, LocalModel: "model-a", RemoteModel: "a"})
	if err != nil {
		t.Fatalf("create map a: %v", err)
	}
	mapB, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: host.ID, LocalModel: "model-b", RemoteModel: "b"})
	if err != nil {
		t.Fatalf("create map b: %v", err)
	}

	m := NewManager(d, nil, nil)
	states := m.getOrCreateStates([]db.RouterMember{
		{ModelMapID: mapA.ID}, {ModelMapID: mapB.ID},
	})

	if states[0].slot != states[1].slot {
		t.Fatal("members on the same host must share one concurrency slot")
	}

	if err := states[0].acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := states[1].acquire(ctx); err == nil {
		t.Fatal("second model on the same host should block while the host slot is held")
	}

	states[0].release()
	if err := states[1].acquire(context.Background()); err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	states[1].release()
}

func TestHostCapacityAllowsParallelRequests(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	host, err := d.Backends.Create(db.BackendInput{Name: "gpu-2", BaseURL: "http://gpu-2", MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	mm, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: host.ID, LocalModel: "model-c", RemoteModel: "c"})
	if err != nil {
		t.Fatalf("create map: %v", err)
	}

	m := NewManager(d, nil, nil)
	s := m.getOrCreateState(mm.ID)
	if err := s.acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := s.acquire(context.Background()); err != nil {
		t.Fatalf("second acquire within capacity 2 failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.acquire(ctx); err == nil {
		t.Fatal("third acquire should block at capacity 2")
	}
	s.release()
	s.release()
}
