package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var hashRef = regexp.MustCompile(`^(?i:sha256[:\s]*)?([0-9a-fA-F]{8,64})$`)

const minHashPrefix = 8

func hashPrefix(ref string) (string, bool) {
	m := hashRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}

func (a *Agent) resolveRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "ev_") || strings.HasPrefix(ref, "sm_") {
		return ref, nil
	}
	p, ok := hashPrefix(ref)
	if !ok {
		return "", fmt.Errorf("%w: %s (use an event id such as ev_..., or a sha256 hash or a prefix of at least %d hex characters)", ErrNotFound, textClip(ref), minHashPrefix)
	}
	ids, err := a.eventsForHash(ctx, p)
	if err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("%w: artifact %s", ErrNotFound, short(p))
	case 1:
		return ids[0], nil
	}
	shown := ids
	if len(shown) > 5 {
		shown = shown[:5]
	}
	return "", fmt.Errorf("hash prefix %s matches %d archived items with different content (%s); use a longer prefix or an event id", p, len(ids), strings.Join(shown, ", "))
}

func textClip(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

func (a *Agent) eventsForHash(ctx context.Context, prefix string) ([]string, error) {
	vis, err := a.visibility(ctx)
	if err != nil {
		return nil, err
	}
	like := prefix + "%"
	rows, err := a.e.db.R.QueryContext(ctx, `
SELECT e.id, e.agent_id, e.scope, r.blob_hash, e.seq FROM blob_refs r JOIN events e ON e.id = r.event_id
 WHERE r.session_id = ? AND r.blob_hash LIKE ?
UNION ALL
SELECT e.id, e.agent_id, e.scope, e.content_hash, e.seq FROM events e
 WHERE e.session_id = ? AND (e.content_hash LIKE ? OR json_extract(e.meta, '$.file_hash') LIKE ? OR json_extract(e.meta, '$.text_hash') LIKE ?)
ORDER BY 5 DESC`, a.s.id, like, a.s.id, like, like, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byContent := map[string]string{}
	var order []string
	seen := map[string]bool{}
	for rows.Next() {
		var id, agent, scope, h string
		var seq int64
		if err := rows.Scan(&id, &agent, &scope, &h, &seq); err != nil {
			return nil, err
		}
		if seen[id] || !vis.allows(a.s.id, agent, scope, id) {
			continue
		}
		seen[id] = true
		key := h
		if c, err := a.contentKey(ctx, id); err == nil {
			key = c
		}
		if _, ok := byContent[key]; ok {
			continue
		}
		byContent[key] = id
		order = append(order, id)
	}
	return order, rows.Err()
}

func (a *Agent) contentKey(ctx context.Context, id string) (string, error) {
	var th string
	err := a.e.db.R.QueryRowContext(ctx, "SELECT COALESCE(json_extract(meta, '$.text_hash'), content_hash) FROM events WHERE id=?", id).Scan(&th)
	return th, err
}
