package godogstats

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/kelseyhightower/envconfig"
)

var varFinder = regexp.MustCompile(`\$\d+`) // matches $0, $1, etc.

const envPrefix = "DOG_STATSD_MOGRIFIER" // prefix for environment variables

type mogrifierEntry struct {
	matcher *regexp.Regexp                                      // the matcher determines whether a mogrifier should run on a metric at all
	handler func(matches []string) (name string, tags []string) // the handler takes the list of matches, and returns metric name and list of tags
}

// mogrifierMap is an ordered map of regular expressions to functions that mogrify a name and return tags
type mogrifierMap []mogrifierEntry

// makePatternHandler returns a function that replaces $0, $1, etc. in the pattern with the corresponding match
func makePatternHandler(pattern string) func([]string) string {
	return func(matches []string) string {
		return varFinder.ReplaceAllStringFunc(pattern, func(s string) string {
			i, err := strconv.Atoi(s[1:])
			if i >= len(matches) || err != nil {
				// Return the original placeholder if the index is out of bounds
				// or the Atoi fails, though given the varFinder regex it should
				// not be possible.
				return s
			}
			return matches[i]
		})
	}
}

// newMogrifierMapFromEnv loads mogrifiers from environment variables
// keys is a list of mogrifier names to load
func newMogrifierMapFromEnv(keys []string) (mogrifierMap, error) {
	mogrifiers := mogrifierMap{}

	type config struct {
		Pattern string            `envconfig:"PATTERN"`
		Tags    map[string]string `envconfig:"TAGS"`
		Name    string            `envconfig:"NAME"`
	}

	for _, mogrifier := range keys {
		cfg := config{}
		if err := envconfig.Process(envPrefix+"_"+mogrifier, &cfg); err != nil {
			return nil, fmt.Errorf("failed to load mogrifier %s: %v", mogrifier, err)
		}

		if cfg.Pattern == "" {
			return nil, fmt.Errorf("no PATTERN specified for mogrifier %s", mogrifier)
		}

		re, err := regexp.Compile(cfg.Pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to compile pattern for %s: %s: %v", mogrifier, cfg.Pattern, err)
		}

		if cfg.Name == "" {
			return nil, fmt.Errorf("no NAME specified for mogrifier %s", mogrifier)
		}

		nameHandler := makePatternHandler(cfg.Name)
		tagHandlers := make(map[string]func([]string) string, len(cfg.Tags))
		for key, value := range cfg.Tags {
			if key == "" {
				return nil, fmt.Errorf("no key specified for tag %s for mogrifier %s", key, mogrifier)
			}
			tagHandlers[key] = makePatternHandler(value)
			if value == "" {
				return nil, fmt.Errorf("no value specified for tag %s for mogrifier %s", key, mogrifier)
			}
		}

		mogrifiers = append(mogrifiers, mogrifierEntry{
			matcher: re,
			handler: func(matches []string) (string, []string) {
				name := nameHandler(matches)
				tags := make([]string, 0, len(tagHandlers))
				for tagKey, handler := range tagHandlers {
					tagValue := handler(matches)
					tags = append(tags, tagKey+":"+tagValue)
				}
				return name, tags
			},
		},
		)

	}
	return mogrifiers, nil
}

// mogrify applies the first mogrifier in the map that matches the name
func (m *mogrifierMap) mogrify(name string) (string, []string) {
	if m == nil {
		return name, nil
	}
	for _, mogrifier := range *m {
		matches := mogrifier.matcher.FindStringSubmatch(name)
		if len(matches) == 0 {
			continue
		}

		mogrifiedName, tags := mogrifier.handler(matches)
		return mogrifiedName, tags
	}

	// no mogrification
	return name, nil
}
