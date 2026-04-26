package creator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ParseFollowingJSON consumes the following.json file produced by
// Instagram's "Download your information" export and returns the entries.
//
// The format Meta ships looks like one of:
//
//   {
//     "relationships_following": [
//       {"string_list_data": [{"value": "natgeo", "timestamp": 1234567890}]},
//       ...
//     ]
//   }
//
// or a bare top-level array. We accept either.
func ParseFollowingJSON(r io.Reader) ([]ImportEntry, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	buf = bytes.TrimSpace(buf)
	if len(buf) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	type stringListItem struct {
		Value     string `json:"value"`
		Timestamp int64  `json:"timestamp"`
	}
	type entry struct {
		StringListData []stringListItem `json:"string_list_data"`
	}

	var raw struct {
		Following []entry `json:"relationships_following"`
	}
	var arr []entry

	switch buf[0] {
	case '{':
		if err := json.Unmarshal(buf, &raw); err != nil {
			return nil, fmt.Errorf("parse object form: %w", err)
		}
		arr = raw.Following
	case '[':
		if err := json.Unmarshal(buf, &arr); err != nil {
			return nil, fmt.Errorf("parse array form: %w", err)
		}
	default:
		return nil, fmt.Errorf("unexpected leading character %q", buf[0])
	}

	var out []ImportEntry
	for _, e := range arr {
		for _, s := range e.StringListData {
			if s.Value == "" {
				continue
			}
			ie := ImportEntry{Handle: s.Value}
			if s.Timestamp > 0 {
				ie.FollowedAt = time.Unix(s.Timestamp, 0)
			}
			out = append(out, ie)
		}
	}
	return out, nil
}
