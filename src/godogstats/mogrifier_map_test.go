package godogstats

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testMogrifier() mogrifierMap {
	return mogrifierMap{
		regexp.MustCompile(`^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`): func(matches []string) (string, []string) {
			name := "ratelimit.service.rate_limit." + matches[3]
			tags := []string{"domain:" + matches[1], "descriptor:" + matches[2]}
			return name, tags
		},
	}
}

func TestMogrify(t *testing.T) {
	m := testMogrifier()
	// Test case 1
	name1 := "ratelimit.service.rate_limit.mongo_cps.database_users.within_limit"
	expectedMogrifiedName1 := "ratelimit.service.rate_limit.within_limit"
	expectedTags1 := []string{"domain:mongo_cps", "descriptor:database_users"}
	mogrifiedName1, tags1 := m.mogrify(name1)
	assert.Equal(t, expectedMogrifiedName1, mogrifiedName1)
	assert.Equal(t, expectedTags1, tags1)
}

func TestEmpty(t *testing.T) {
	m := mogrifierMap{}
	name, tags := m.mogrify("ratelimit.service.rate_limit.mongo_cps.database_users.within_limit")
	assert.Equal(t, "ratelimit.service.rate_limit.mongo_cps.database_users.within_limit", name)
	assert.Empty(t, tags)
}

func TestNil(t *testing.T) {
	var m mogrifierMap
	name, tags := m.mogrify("ratelimit.service.rate_limit.mongo_cps.database_users.within_limit")
	assert.Equal(t, "ratelimit.service.rate_limit.mongo_cps.database_users.within_limit", name)
	assert.Empty(t, tags)
}

func TestLoadMogrifiersFromEnv(t *testing.T) {
	// Test case 1
	pattern := `^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`
	t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", pattern)
	t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "ratelimit.service.rate_limit.$3")
	t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", "domain:$1,descriptor:$2")
	mogrifiers, err := newMogrifierMapFromEnv([]string{"TAG"})
	assert.NoError(t, err)
	assert.NotNil(t, mogrifiers)
	assert.Len(t, mogrifiers, 1)

	name, tags := mogrifiers.mogrify("ratelimit.service.rate_limit.mongo_cps.database_users.within_limit")
	assert.Equal(t, name, "ratelimit.service.rate_limit.within_limit")
	assert.ElementsMatch(t, []string{"domain:mongo_cps", "descriptor:database_users"}, tags)
}

func TestValidation(t *testing.T) {
	t.Run("No settings will fail", func(t *testing.T) {
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.Error(t, err)
	})

	t.Run("EmptyPattern", func(t *testing.T) {
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", "")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "ratelimit.service.rate_limit.$3")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", "domain:$1,descriptor:$2")
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.Error(t, err)
	})

	t.Run("EmptyName", func(t *testing.T) {
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", `^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`)
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", "domain:$1,descriptor:$2")
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.Error(t, err)
	})

	t.Run("EmptyTagKey", func(t *testing.T) {
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", `^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`)
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "ratelimit.service.rate_limit.$3")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", ":5")
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.Error(t, err)
	})

	t.Run("EmptyTagValue", func(t *testing.T) {
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", `^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`)
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "ratelimit.service.rate_limit.$3")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", "domain:$1,descriptor:")
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.Error(t, err)
	})

	t.Run("Success w/ No mogrifiers", func(t *testing.T) {
		_, err := newMogrifierMapFromEnv([]string{})
		assert.NoError(t, err)
	})

	t.Run("Success w/ mogrifier", func(t *testing.T) {
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_PATTERN", `^ratelimit\.service\.rate_limit\.(.*)\.(.*)\.(.*)$`)
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_NAME", "ratelimit.service.rate_limit.$3")
		t.Setenv("DOG_STATSD_MOGRIFIER_TAG_TAGS", "domain:$1,descriptor:$2")
		_, err := newMogrifierMapFromEnv([]string{"TAG"})
		assert.NoError(t, err)
	})
}
