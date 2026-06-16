package filter_test

// Partition table (ECP + BVA) for loader_test.go
// step: 01-file-filter-provider
//
// === LoadFromFile (path) ===
// 参数 / 状态         | 等价类                     | 类型        | 代表值                                          | 期望输出
// path               | 不存在                     | 无效        | "<tmp>/nope.yaml"                              | err != nil, filters == nil
// path content       | 全空 YAML "{}"              | 有效·下边界 | "{}"                                           | ip+uid filter 都为空（Match -> None）
// path content       | 仅 ip section              | 有效        | "ip:\n  allow: [192.168.0.0/24]"               | ip filter 生效；uid filter 为空
// path content       | 仅 uid section             | 有效        | "uid:\n  allow: [\"1001\"]"                    | uid filter 生效；ip filter 为空
// path content       | ip+uid 双 allow+deny      | 有效        | full sample                                    | 两个 filter 都生效
// path content       | YAML 语法错               | 无效        | "ip: [unclosed"                                | err != nil
// path content       | ip.allow 含非法 CIDR      | 无效        | "ip:\n  allow: [not-a-cidr]"                   | err != nil
// path content       | ip.deny  含非法 CIDR      | 无效        | "ip:\n  deny: [9999.9.9.9/8]"                  | err != nil
//
// === ParseSnapshot (snap) ===
// 参数 / 状态         | 等价类                     | 类型        | 代表值                                          | 期望输出
// snap.IP.Allow      | 空 slice                  | 有效·下边界 | nil / []                                       | 构造成功；ipFilter.Match(任意 ip) != Allow
// snap.IP.Allow      | 单条合法 CIDR             | 有效        | ["192.168.0.0/24"]                             | Match("192.168.0.1") == Allow
// snap.IP.Allow      | 合法 + 空字符串混合       | 有效（trim）| ["10.0.0.0/8", ""]                             | 空字符串跳过；Match("10.0.0.1") == Allow
// snap.IP.Allow      | 含非法 CIDR              | 无效        | ["not-a-cidr"]                                 | err != nil
// snap.IP.Deny       | 单条合法 CIDR             | 有效        | ["1.2.3.4/32"]                                 | Match("1.2.3.4") == Deny
// snap.IP.Deny       | 含非法 CIDR              | 无效        | ["bogus"]                                      | err != nil
// snap.UID.Allow     | 单条                      | 有效        | ["1001"]                                       | Match("1001") == Allow
// snap.UID.Allow     | 含空字符串                | 有效（trim）| ["1001", ""]                                   | 空字符串跳过；Match("1001") == Allow
// snap.UID.Deny      | 单条                      | 有效        | ["999"]                                        | Match("999") == Deny
// snap.UID 全空      | 空 sections               | 有效·下边界 | UIDSection{}                                   | Match("anyone") == None
//
// 契约来源: requirement_text 行为契约 #1/#2/#3, 接口契约 LoadFromFile / ParseSnapshot 签名

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/envoyproxy/ratelimit/src/filter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "filter.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// --- LoadFromFile error paths ---

// ECP row: path / 不存在 / 无效
func TestLoadFromFile_FileMissing_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.yaml")

	ip, uid, err := filter.LoadFromFile(missing)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// ECP row: path content / YAML 语法错 / 无效
func TestLoadFromFile_YAMLSyntaxError_ReturnsError(t *testing.T) {
	p := writeYAML(t, "ip: [unclosed\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// ECP row: path content / ip.allow 含非法 CIDR / 无效
func TestLoadFromFile_InvalidIPAllowCIDR_ReturnsError(t *testing.T) {
	p := writeYAML(t, "ip:\n  allow:\n    - not-a-cidr\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// ECP row: path content / ip.deny 含非法 CIDR / 无效
func TestLoadFromFile_InvalidIPDenyCIDR_ReturnsError(t *testing.T) {
	p := writeYAML(t, "ip:\n  deny:\n    - 9999.9.9.9/8\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// --- LoadFromFile happy paths ---

// ECP row: path content / 全空 YAML "{}" / 有效·下边界
func TestLoadFromFile_EmptyYAML_ReturnsEmptyFilters(t *testing.T) {
	p := writeYAML(t, "{}\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.NoError(t, err)
	require.NotNil(t, ip)
	require.NotNil(t, uid)

	act, _ := ip.Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionNone, act, "empty config => ip.Match returns None")

	act, _ = uid.Match("1001")
	assert.Equal(t, filter.FilterActionNone, act, "empty config => uid.Match returns None")
}

// ECP row: path content / 仅 ip section / 有效
func TestLoadFromFile_OnlyIPSection_UIDFilterIsEmpty(t *testing.T) {
	p := writeYAML(t, "ip:\n  allow:\n    - 192.168.0.0/24\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.NoError(t, err)
	require.NotNil(t, ip)
	require.NotNil(t, uid)

	act, _ := ip.Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act)

	act, _ = uid.Match("anything")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: path content / 仅 uid section / 有效
func TestLoadFromFile_OnlyUIDSection_IPFilterIsEmpty(t *testing.T) {
	p := writeYAML(t, "uid:\n  allow:\n    - \"1001\"\n")

	ip, uid, err := filter.LoadFromFile(p)

	require.NoError(t, err)
	require.NotNil(t, ip)
	require.NotNil(t, uid)

	act, _ := uid.Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act)

	act, _ = ip.Match("10.0.0.1")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: path content / ip+uid 双 allow+deny / 有效
func TestLoadFromFile_FullYAML_AllSectionsTakeEffect(t *testing.T) {
	body := `ip:
  allow:
    - 192.168.0.0/24
    - 10.0.0.0/8
  deny:
    - 1.2.3.4/32
uid:
  allow:
    - "1001"
    - "1002"
  deny:
    - "999"
`
	p := writeYAML(t, body)

	ip, uid, err := filter.LoadFromFile(p)
	require.NoError(t, err)
	require.NotNil(t, ip)
	require.NotNil(t, uid)

	// ip.allow path
	act, _ := ip.Match("192.168.0.5")
	assert.Equal(t, filter.FilterActionAllow, act, "192.168.0.5 in 192.168.0.0/24 => Allow")
	act, _ = ip.Match("10.5.5.5")
	assert.Equal(t, filter.FilterActionAllow, act, "10.5.5.5 in 10.0.0.0/8 => Allow")

	// ip.deny path
	act, _ = ip.Match("1.2.3.4")
	assert.Equal(t, filter.FilterActionDeny, act, "1.2.3.4 in deny list => Deny")

	// ip none path
	act, _ = ip.Match("8.8.8.8")
	assert.Equal(t, filter.FilterActionNone, act, "8.8.8.8 in neither => None")

	// uid paths
	act, _ = uid.Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act)
	act, _ = uid.Match("1002")
	assert.Equal(t, filter.FilterActionAllow, act)
	act, _ = uid.Match("999")
	assert.Equal(t, filter.FilterActionDeny, act)
	act, _ = uid.Match("12345")
	assert.Equal(t, filter.FilterActionNone, act)
}

// --- ParseSnapshot paths (no file IO) ---

// ECP row: snap.IP.Allow / 空 slice / 有效·下边界 + snap.UID 全空 / 有效·下边界
func TestParseSnapshot_AllEmpty_ProducesEmptyFilters(t *testing.T) {
	ip, uid, err := filter.ParseSnapshot(filter.FilterSnapshot{})

	require.NoError(t, err)
	require.NotNil(t, ip)
	require.NotNil(t, uid)

	act, _ := ip.Match("1.2.3.4")
	assert.Equal(t, filter.FilterActionNone, act)
	act, _ = uid.Match("anyone")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: snap.IP.Allow / 单条合法 CIDR / 有效
func TestParseSnapshot_IPAllowSingleValid_MatchesAllow(t *testing.T) {
	snap := filter.FilterSnapshot{
		IP: filter.IPSection{Allow: []string{"192.168.0.0/24"}},
	}

	ip, _, err := filter.ParseSnapshot(snap)
	require.NoError(t, err)
	require.NotNil(t, ip)

	act, _ := ip.Match("192.168.0.1")
	assert.Equal(t, filter.FilterActionAllow, act)

	act, _ = ip.Match("10.0.0.1")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: snap.IP.Allow / 合法 + 空字符串混合 / 有效（trim）
func TestParseSnapshot_IPAllowWithEmptyString_SkipsEmpty(t *testing.T) {
	snap := filter.FilterSnapshot{
		IP: filter.IPSection{Allow: []string{"10.0.0.0/8", ""}},
	}

	ip, _, err := filter.ParseSnapshot(snap)
	require.NoError(t, err, "empty string should be skipped, not error")
	require.NotNil(t, ip)

	act, _ := ip.Match("10.5.5.5")
	assert.Equal(t, filter.FilterActionAllow, act)
}

// ECP row: snap.IP.Allow / 含非法 CIDR / 无效
func TestParseSnapshot_IPAllowInvalidCIDR_ReturnsError(t *testing.T) {
	snap := filter.FilterSnapshot{
		IP: filter.IPSection{Allow: []string{"not-a-cidr"}},
	}

	ip, uid, err := filter.ParseSnapshot(snap)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// ECP row: snap.IP.Deny / 单条合法 CIDR / 有效
func TestParseSnapshot_IPDenySingleValid_MatchesDeny(t *testing.T) {
	snap := filter.FilterSnapshot{
		IP: filter.IPSection{Deny: []string{"1.2.3.4/32"}},
	}

	ip, _, err := filter.ParseSnapshot(snap)
	require.NoError(t, err)
	require.NotNil(t, ip)

	act, _ := ip.Match("1.2.3.4")
	assert.Equal(t, filter.FilterActionDeny, act)

	act, _ = ip.Match("1.2.3.5")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: snap.IP.Deny / 含非法 CIDR / 无效
func TestParseSnapshot_IPDenyInvalidCIDR_ReturnsError(t *testing.T) {
	snap := filter.FilterSnapshot{
		IP: filter.IPSection{Deny: []string{"bogus"}},
	}

	ip, uid, err := filter.ParseSnapshot(snap)

	require.Error(t, err)
	assert.Nil(t, ip)
	assert.Nil(t, uid)
}

// ECP row: snap.UID.Allow / 单条 / 有效
func TestParseSnapshot_UIDAllowSingle_MatchesAllow(t *testing.T) {
	snap := filter.FilterSnapshot{
		UID: filter.UIDSection{Allow: []string{"1001"}},
	}

	_, uid, err := filter.ParseSnapshot(snap)
	require.NoError(t, err)
	require.NotNil(t, uid)

	act, _ := uid.Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act)
	act, _ = uid.Match("1002")
	assert.Equal(t, filter.FilterActionNone, act)
}

// ECP row: snap.UID.Allow / 含空字符串 / 有效（trim）
func TestParseSnapshot_UIDAllowWithEmptyString_SkipsEmpty(t *testing.T) {
	snap := filter.FilterSnapshot{
		UID: filter.UIDSection{Allow: []string{"1001", ""}},
	}

	_, uid, err := filter.ParseSnapshot(snap)
	require.NoError(t, err, "empty UID string should be skipped, not error")
	require.NotNil(t, uid)

	act, _ := uid.Match("1001")
	assert.Equal(t, filter.FilterActionAllow, act)
	// empty string must not be treated as a real entry that allows ""
	act, _ = uid.Match("")
	assert.Equal(t, filter.FilterActionNone, act, "empty string entry must be skipped")
}

// ECP row: snap.UID.Deny / 单条 / 有效
func TestParseSnapshot_UIDDenySingle_MatchesDeny(t *testing.T) {
	snap := filter.FilterSnapshot{
		UID: filter.UIDSection{Deny: []string{"999"}},
	}

	_, uid, err := filter.ParseSnapshot(snap)
	require.NoError(t, err)
	require.NotNil(t, uid)

	act, _ := uid.Match("999")
	assert.Equal(t, filter.FilterActionDeny, act)
	act, _ = uid.Match("1000")
	assert.Equal(t, filter.FilterActionNone, act)
}
