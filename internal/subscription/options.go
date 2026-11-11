package subscription

import (
	"math/rand/v2"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/ExpTechTW/proxygate/internal/config"
)

type nodeOptions struct {
	count     int
	countries map[string]struct{}
	exclude   map[string]struct{}
	slots     map[int]struct{}
	shuffle   bool
	group     string
	sortBy    string
	preset    string
}

func nodeOptionsFromQuery(query url.Values) nodeOptions {
	options := nodeOptions{
		countries: parseCountrySet(query.Get("countries")),
		exclude:   parseCountrySet(firstNonEmpty(query.Get("exclude"), query.Get("excludeCountries"))),
		slots:     parseSlotSet(firstNonEmpty(query.Get("slots"), query.Get("slot"))),
		shuffle:   truthy(query.Get("shuffle")),
		group:     strings.ToLower(strings.TrimSpace(query.Get("group"))),
		sortBy:    strings.ToLower(strings.TrimSpace(query.Get("sort"))),
	}
	if len(options.countries) == 0 {
		if set := parseCountrySet(query.Get("country")); len(set) > 0 {
			options.countries = set
		}
	}
	if count, err := strconv.Atoi(strings.TrimSpace(query.Get("count"))); err == nil {
		options.count = count
	}
	switch preset := strings.ToLower(strings.TrimSpace(query.Get("preset"))); preset {
	case "stable", "quality":
		options.preset = preset
		if options.sortBy == "" {
			options.sortBy = preset
		}
		if _, explicit := query["count"]; !explicit {
			options.count = 20
		}
	}
	return options
}

// applyClientPresets folds a per-client preset into the request options.
// Explicit query parameters always win; presets only fill empty slots.
func applyClientPresets(options nodeOptions, client config.SubscriptionClient) nodeOptions {
	if options.count <= 0 && client.Count > 0 {
		options.count = client.Count
	}
	if len(options.countries) == 0 && len(client.Countries) > 0 {
		options.countries = parseCountryList(client.Countries)
	}
	if len(options.exclude) == 0 && len(client.Exclude) > 0 {
		options.exclude = parseCountryList(client.Exclude)
	}
	if client.Shuffle {
		options.shuffle = true
	}
	if options.group == "" {
		options.group = strings.ToLower(strings.TrimSpace(client.Group))
	}
	if options.sortBy == "" {
		options.sortBy = strings.ToLower(strings.TrimSpace(client.Sort))
	}
	return options
}

func (options *nodeOptions) applyDefaults() {
	if options.group == "" {
		options.group = "country"
	}
	if options.sortBy == "" {
		options.sortBy = "slot"
	}
}

func parseCountryList(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		code := strings.ToUpper(strings.TrimSpace(value))
		if len(code) == 2 {
			set[code] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func parseCountrySet(raw string) map[string]struct{} {
	fields := strings.FieldsFunc(raw, func(symbol rune) bool {
		return symbol == ',' || symbol == ';' || symbol == ' ' || symbol == '\t' || symbol == '\n' || symbol == '|'
	})
	if len(fields) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		code := strings.ToUpper(strings.TrimSpace(field))
		if len(code) == 2 {
			set[code] = struct{}{}
		}
	}
	return set
}

// parseSlotSet accepts an explicit slot selection such as "3,7,11-15" so a
// subscription can expose exactly the slots the user checked in the panel.
func parseSlotSet(raw string) map[int]struct{} {
	fields := strings.FieldsFunc(raw, func(symbol rune) bool {
		return symbol == ',' || symbol == ';' || symbol == ' ' || symbol == '\t' || symbol == '\n' || symbol == '|'
	})
	if len(fields) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if start, end, ok := strings.Cut(field, "-"); ok {
			from, errFrom := strconv.Atoi(strings.TrimSpace(start))
			to, errTo := strconv.Atoi(strings.TrimSpace(end))
			if errFrom != nil || errTo != nil || from < 1 || to < from || to-from > 512 {
				continue
			}
			for id := from; id <= to; id++ {
				set[id] = struct{}{}
			}
			continue
		}
		if id, err := strconv.Atoi(field); err == nil && id > 0 {
			set[id] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

func truthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "shuffle":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// selectNodes applies the subscription builder options to the full node set.
// Country filters are strict: a selected country only ever yields nodes from
// that country, so the subscription mirrors exactly what the user checked
// instead of silently falling back to unrelated nodes.
func selectNodes(nodes []trojanNode, options nodeOptions) []trojanNode {
	result := make([]trojanNode, 0, len(nodes))
	for _, node := range nodes {
		code := strings.ToUpper(node.country)
		if len(options.slots) > 0 {
			if _, ok := options.slots[node.slotID]; !ok {
				continue
			}
		}
		if len(options.countries) > 0 {
			if _, ok := options.countries[code]; !ok {
				continue
			}
		}
		if _, excluded := options.exclude[code]; excluded {
			continue
		}
		result = append(result, node)
	}
	switch options.sortBy {
	case "country":
		sort.SliceStable(result, func(left, right int) bool {
			if result[left].country != result[right].country {
				return result[left].country < result[right].country
			}
			return result[left].name < result[right].name
		})
	case "name":
		sort.SliceStable(result, func(left, right int) bool { return result[left].name < result[right].name })
	case "stable":
		sort.SliceStable(result, func(left, right int) bool {
			if result[left].failCount != result[right].failCount {
				return result[left].failCount < result[right].failCount
			}
			if result[left].successCount != result[right].successCount {
				return result[left].successCount > result[right].successCount
			}
			if result[left].quality != result[right].quality {
				return result[left].quality > result[right].quality
			}
			return result[left].name < result[right].name
		})
	case "quality":
		sort.SliceStable(result, func(left, right int) bool {
			if result[left].quality != result[right].quality {
				return result[left].quality > result[right].quality
			}
			if result[left].failCount != result[right].failCount {
				return result[left].failCount < result[right].failCount
			}
			if result[left].successCount != result[right].successCount {
				return result[left].successCount > result[right].successCount
			}
			return result[left].name < result[right].name
		})
	}
	if options.shuffle {
		rand.Shuffle(len(result), func(left, right int) {
			result[left], result[right] = result[right], result[left]
		})
	}
	if options.count > 0 && options.count < len(result) {
		result = result[:options.count]
	}
	return result
}
