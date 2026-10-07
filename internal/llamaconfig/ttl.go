package llamaconfig

import "gopkg.in/yaml.v3"

// TTLs reads each model's idle unload time, in seconds, from llama-swap's
// config. 0 means the model is never unloaded for being idle. A model
// without its own "ttl", or with -1, uses the top-level "globalTTL".
func TTLs(content string) (map[string]int, error) {
	var doc struct {
		GlobalTTL int `yaml:"globalTTL"`
		Models    map[string]struct {
			TTL *int `yaml:"ttl"`
		} `yaml:"models"`
	}
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, err
	}
	out := make(map[string]int, len(doc.Models))
	for id, m := range doc.Models {
		ttl := doc.GlobalTTL
		if m.TTL != nil && *m.TTL != -1 {
			ttl = *m.TTL
		}
		out[id] = max(ttl, 0)
	}
	return out, nil
}
