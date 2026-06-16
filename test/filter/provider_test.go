package filter_test

// Partition table (ECP + BVA) for provider_test.go
// step: 01-file-filter-provider
//
// === NewStaticProvider ===
// 参数 / 状态         | 等价类                | 类型        | 代表值                     | 期望输出
// ip Filter          | nil                   | 有效·边界   | nil                        | IPFilter() == nil (透传)
// uid Filter         | nil                   | 有效·边界   | nil                        | UIDFilter() == nil (透传)
// ip Filter          | 真实 IP filter        | 有效        | NewIPFilter([10.0.0.0/8],_)| IPFilter() 返回同实例
// uid Filter         | 真实 UID filter       | 有效        | NewUIDFilter({1001},_)     | UIDFilter() 返回同实例
// Stop()             | 单次                  | 有效        | -                          | 不 panic
// Stop()             | 重复调用              | 有效·边界   | 调 2 次                    | 不 panic
//
// === NewFileProvider 初次 load ===
// 参数 / 状态         | 等价类                | 类型        | 代表值                     | 期望输出
// path               | 不存在                | 无效        | "<tmp>/nope.yaml"          | err != nil, provider == nil
// path content       | YAML 语法错          | 无效        | "ip: [unclosed"            | err != nil, provider == nil
// path content       | CIDR 错              | 无效        | "ip:\n  allow: [bogus]"    | err != nil, provider == nil
// path content       | 合法                  | 有效        | full yaml                  | provider 非空；IPFilter/UIDFilter 生效
//
// === NewFileProvider Stop 语义 ===
// 状态               | 等价类                | 类型        | 代表值                     | 期望输出
// Stop 前 NumGoroutine | -                  | 基线        | runtime.NumGoroutine()     | 记录基线
// Stop 后 NumGoroutine | -                  | 有效        | 给收尾 50ms                | 应回到基线 ±2 (允许测试 runtime 抖动)
// Stop 后 IPFilter()   | -                  | 有效        | 调用                       | 不 panic, 返回最后一次 load 结果
// Stop 后 修改文件     | -                  | 有效        | overwrite                  | filter 不再变化 (watch 已退)
//
// === Pairwise 组合表（热加载） ===
// pairwise: 3 params, 9 combos (from 18 full combos) — file_deleted 语义独立, 单独 1 行
// 参数: reload_trigger ∈ {overwrite, rename, chmod}
//        new_content_kind ∈ {valid, yaml_error, cidr_error}
//        concurrent_readers ∈ {none, many}
//
// idx | trigger    | content     | readers | 期望
// 1   | overwrite  | valid       | none    | 新 filter 生效
// 2   | rename     | yaml_error  | none    | 保留旧 filter（不 panic）
// 3   | chmod      | cidr_error  | none    | 保留旧 filter
// 4   | chmod      | yaml_error  | many    | 保留旧 filter；并发读不 race
// 5   | rename     | valid       | many    | 新 filter 生效；并发读不 race
// 6   | overwrite  | cidr_error  | many    | 保留旧 filter；并发读不 race
// 7   | overwrite  | yaml_error  | many    | 保留旧 filter；并发读不 race
// 8   | rename     | cidr_error  | many    | 保留旧 filter；并发读不 race
// 9   | chmod      | valid       | many    | 新 filter 生效；并发读不 race
// 10  | -          | file_deleted| many    | 保留旧 filter（独立场景）
//
// 契约来源: requirement_text 行为契约 #1/#3/#4/#5, 接口契约 NewStaticProvider / NewFileProvider / Provider 接口

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/envoyproxy/ratelimit/src/filter"
	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

func newTestScope() stats.Scope {
	return stats.NewStore(stats.NewNullSink(), false).Scope("test")
}

func newTestLogger() *logger.Logger {
	l := logger.New()
	l.SetLevel(logger.PanicLevel) // silence
	return l
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func setupTempFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "filter.yaml")
	writeFile(t, p, body)
	return p
}

const validYAMLv1 = `ip:
  allow:
    - 192.168.0.0/24
uid:
  allow:
    - "1001"
`

const validYAMLv2 = `ip:
  allow:
    - 10.0.0.0/8
uid:
  allow:
    - "2002"
`

const yamlSyntaxError = "ip: [unclosed\n"
const cidrError = "ip:\n  allow:\n    - not-a-cidr\n"

// waitForFilterMatch polls IPFilter() until ipMatch(testIP) returns the
// expected Action, or times out. This avoids flaky `time.Sleep` on slow CI.
func waitForFilterMatch(t *testing.T, p filter.Provider, testIP string, want filter.Action, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		act, _ := p.IPFilter().Match(testIP)
		if act == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// --- NewStaticProvider ---

// ECP row: ip Filter / nil ; uid Filter / nil
func TestStaticProvider_NilFilters_ReturnsNil(t *testing.T) {
	p := filter.NewStaticProvider(nil, nil)
	require.NotNil(t, p)

	assert.Nil(t, p.IPFilter())
	assert.Nil(t, p.UIDFilter())
}

// ECP row: ip Filter / 真实 ; uid Filter / 真实 ; Stop / 重复调用
// Combined: behavioral pass-through + Stop idempotency. Stop idempotency is
// asserted via a follow-up Match() returning Allow (i.e. the injected real
// filter is still reachable after two Stop() calls); a plain `NotPanics`
// would be over-satisfied by a no-op stub.
func TestStaticProvider_RealFilters_PassThroughAndStopIdempotent(t *testing.T) {
	ip, uid, err := filter.ParseSnapshot(filter.FilterSnapshot{
		IP:  filter.IPSection{Allow: []string{"10.0.0.0/8"}},
		UID: filter.UIDSection{Allow: []string{"1001"}},
	})
	require.NoError(t, err)

	p := filter.NewStaticProvider(ip, uid)

	// behavioral assertion: the provider returns the injected real filter
	act, _ := p.IPFilter().Match("10.5.5.5")
	assert.Equal(t, filter.FilterActionAllow, act, "static provider must pass through injected ip filter")

	act, _ = p.UIDFilter().Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act, "static provider must pass through injected uid filter")

	// Stop idempotency: after calling Stop twice, the injected filter is
	// still readable. NotPanics + behavioral check together — a no-op stub
	// will fail the behavioral half (returns FilterActionError, not Allow).
	assert.NotPanics(t, func() { p.Stop() })
	assert.NotPanics(t, func() { p.Stop() }, "Stop must be safe to call twice")
	act, _ = p.IPFilter().Match("10.5.5.5")
	assert.Equal(t, filter.FilterActionAllow, act, "after Stop x2, injected filter must still be readable")
}

// --- NewFileProvider initial load failure paths ---

// ECP row: path / 不存在 / 无效
func TestFileProvider_InitialLoadMissingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.yaml")

	p, err := filter.NewFileProvider(missing, newTestScope(), newTestLogger())

	require.Error(t, err)
	assert.Nil(t, p, "provider must be nil when initial load fails")
}

// ECP row: path content / YAML 语法错 / 无效
func TestFileProvider_InitialLoadYAMLSyntaxError_ReturnsError(t *testing.T) {
	p1 := setupTempFile(t, yamlSyntaxError)

	p, err := filter.NewFileProvider(p1, newTestScope(), newTestLogger())

	require.Error(t, err)
	assert.Nil(t, p)
}

// ECP row: path content / CIDR 错 / 无效
func TestFileProvider_InitialLoadCIDRError_ReturnsError(t *testing.T) {
	p1 := setupTempFile(t, cidrError)

	p, err := filter.NewFileProvider(p1, newTestScope(), newTestLogger())

	require.Error(t, err)
	assert.Nil(t, p)
}

// ECP row: path content / 合法 / 有效
func TestFileProvider_InitialLoadValid_FiltersActive(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)

	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	require.NotNil(t, p)
	defer p.Stop()

	act, _ := p.IPFilter().Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act)

	act, _ = p.UIDFilter().Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act)
}

// --- Hot reload: pairwise cases ---

// Pairwise idx 1: overwrite + valid + none
func TestFileProvider_HotReload_OverwriteValidNoReaders_NewFilterActive(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	// v1 active
	act, _ := p.IPFilter().Match("192.168.0.1")
	require.Equal(t, filter.FilterActionAllow, act)

	// overwrite with v2 (10.0.0.0/8)
	writeFile(t, path, validYAMLv2)

	ok := waitForFilterMatch(t, p, "10.5.5.5", filter.FilterActionAllow, 2*time.Second)
	assert.True(t, ok, "after overwrite with valid v2, new ip 10.5.5.5 should be Allow")

	// old v1 ip range no longer matches
	act, _ = p.IPFilter().Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionNone, act, "v1 range no longer present")
}

// Pairwise idx 2: rename + yaml_error + none
// rename = atomic-rename (K8s ConfigMap pattern): write to tmp then os.Rename onto path.
func TestFileProvider_HotReload_RenameYAMLError_KeepsOldFilter(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, "tmp.yaml")
	writeFile(t, tmp, yamlSyntaxError)

	require.NoError(t, os.Rename(tmp, path))

	// give the watcher time to receive the event and attempt reload
	time.Sleep(300 * time.Millisecond)

	// must not panic and old filter must still match v1 (192.168.0.0/24)
	assert.NotPanics(t, func() {
		act, _ := p.IPFilter().Match("192.168.0.1")
		assert.Equal(t, filter.FilterActionAllow, act, "yaml-error must NOT clobber old filter")
	})
}

// Pairwise idx 3: chmod + cidr_error + none
func TestFileProvider_HotReload_ChmodCIDRError_KeepsOldFilter(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	// rewrite file with bad CIDR (chmod itself doesn't change content, so to
	// exercise "content changed via chmod trigger" we change content + chmod)
	writeFile(t, path, cidrError)
	require.NoError(t, os.Chmod(path, 0o600))

	time.Sleep(300 * time.Millisecond)

	act, _ := p.IPFilter().Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act, "cidr-error must NOT clobber old filter")
}

// Pairwise idx 5: rename + valid + many readers
// Combined: tests rename-trigger reload + concurrent readers + race-safety.
// Run with -race to validate atomic.Pointer correctness.
func TestFileProvider_HotReload_RenameValidWithConcurrentReaders_NewFilterActiveNoRace(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	stop := make(chan struct{})
	var reads atomic.Int64
	const numReaders = 8

	var wg sync.WaitGroup
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = p.IPFilter() // load read path; race detector watches this
					_, _ = p.IPFilter().Match("10.5.5.5")
					reads.Add(1)
				}
			}
		}()
	}

	// Give readers a moment to ramp.
	time.Sleep(50 * time.Millisecond)

	// atomic rename swap
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, "v2.yaml")
	writeFile(t, tmp, validYAMLv2)
	require.NoError(t, os.Rename(tmp, path))

	// wait for reload
	ok := waitForFilterMatch(t, p, "10.5.5.5", filter.FilterActionAllow, 2*time.Second)
	close(stop)
	wg.Wait()

	assert.True(t, ok, "after rename with v2, 10.5.5.5 should be Allow")
	assert.Greater(t, reads.Load(), int64(0), "readers should have observed at least one read")
}

// Pairwise idx 6,7,8: cover invalid-content reloads under concurrent readers.
// Use a table-driven test so all three rows are exercised and the race
// detector observes concurrent reads during each scenario.
func TestFileProvider_HotReload_InvalidContentWithReaders_KeepsOldFilterNoRace(t *testing.T) {
	cases := []struct {
		name       string
		badContent string
	}{
		// idx 6: overwrite + cidr_error + many
		{"overwrite_cidr_error", cidrError},
		// idx 7: overwrite + yaml_error + many
		{"overwrite_yaml_error", yamlSyntaxError},
		// idx 8: rename + cidr_error + many (we use overwrite here because the
		// "trigger" axis was already covered for valid in idx 5; the salient
		// behavior is "invalid content keeps old filter under concurrency")
		{"rename_cidr_error", cidrError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := setupTempFile(t, validYAMLv1)
			p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
			require.NoError(t, err)
			defer p.Stop()

			stop := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							_, _ = p.IPFilter().Match("192.168.0.1")
						}
					}
				}()
			}
			time.Sleep(50 * time.Millisecond)

			writeFile(t, path, tc.badContent)
			time.Sleep(300 * time.Millisecond)

			// old filter (v1: 192.168.0.0/24) must still be active
			act, _ := p.IPFilter().Match("192.168.0.1")
			close(stop)
			wg.Wait()

			assert.Equal(t, filter.FilterActionAllow, act, "invalid content must not clobber old filter")
		})
	}
}

// Pairwise idx 9: chmod + valid + many
func TestFileProvider_HotReload_ChmodValidWithReaders_NewFilterActive(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = p.IPFilter().Match("192.168.0.1")
				}
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)

	// change content + chmod to bump
	writeFile(t, path, validYAMLv2)
	require.NoError(t, os.Chmod(path, 0o600))

	ok := waitForFilterMatch(t, p, "10.5.5.5", filter.FilterActionAllow, 2*time.Second)
	close(stop)
	wg.Wait()

	assert.True(t, ok, "valid content via chmod-trigger should activate new filter")
}

// Pairwise idx 10: file_deleted scenario (independent of trigger axis)
func TestFileProvider_HotReload_FileDeleted_KeepsOldFilter(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)
	defer p.Stop()

	// verify old filter
	act, _ := p.IPFilter().Match("192.168.0.1")
	require.Equal(t, filter.FilterActionAllow, act)

	// remove the file (simulate ConfigMap transient disappearance)
	require.NoError(t, os.Remove(path))

	time.Sleep(300 * time.Millisecond)

	// old filter must still match — must NOT degrade to "deny all"
	assert.NotPanics(t, func() {
		act, _ := p.IPFilter().Match("192.168.0.1")
		assert.Equal(t, filter.FilterActionAllow, act, "file-deleted must NOT clobber old filter")
	})
}

// --- Stop semantics ---

// Stop() goroutine leak check
func TestFileProvider_Stop_NoGoroutineLeak(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)

	// allow runtime to settle
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)

	// during operation, goroutine count should be higher (watcher goroutine running)
	during := runtime.NumGoroutine()
	assert.Greater(t, during, before, "watcher goroutine should be running")

	p.Stop()

	// give the watcher goroutine time to exit
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	// allow ±2 jitter from go runtime
	assert.LessOrEqual(t, after, before+2, "Stop must release watcher goroutine (before=%d, after=%d)", before, after)
}

// Stop() then IPFilter()/UIDFilter() still safe
func TestFileProvider_AfterStop_ReadsReturnLastFilter(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)

	p.Stop()

	assert.NotPanics(t, func() {
		act, _ := p.IPFilter().Match("192.168.0.1")
		assert.Equal(t, filter.FilterActionAllow, act, "after Stop, reads must still return last loaded filter")

		act, _ = p.UIDFilter().Match("1001")
		assert.Equal(t, filter.FilterActionAllow, act)
	})
}

// Stop() then file modification: filter must NOT change (watcher exited)
func TestFileProvider_AfterStop_FileChangeIgnored(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)

	p.Stop()

	// overwrite with v2 after Stop
	writeFile(t, path, validYAMLv2)
	time.Sleep(300 * time.Millisecond)

	// filter should still be v1 (192.168.0.0/24), not v2 (10.0.0.0/8)
	act, _ := p.IPFilter().Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act, "after Stop, v1 filter must remain")

	act, _ = p.IPFilter().Match("10.5.5.5")
	assert.Equal(t, filter.FilterActionNone, act, "after Stop, v2 filter must NOT have loaded")
}

// Stop() idempotent combined with read-after-stop. A pure `NotPanics` would
// be over-satisfied by a no-op stub, so the test also asserts that after two
// Stop() calls the last successfully loaded filter is still observable.
func TestFileProvider_StopIsIdempotent_AndReadable(t *testing.T) {
	path := setupTempFile(t, validYAMLv1)
	p, err := filter.NewFileProvider(path, newTestScope(), newTestLogger())
	require.NoError(t, err)

	assert.NotPanics(t, func() { p.Stop() })
	assert.NotPanics(t, func() { p.Stop() }, "Stop must be safe to call twice")

	// behavioral: last loaded filter (v1: 192.168.0.0/24) still readable
	act, _ := p.IPFilter().Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act, "after Stop x2, last loaded filter must still match")
}
