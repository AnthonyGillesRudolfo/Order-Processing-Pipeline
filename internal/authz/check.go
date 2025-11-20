package authz

import (
    "context"
    "log"
    "net/http"
)

// PrincipalFromRequest extracts the effective principal.
// Order of precedence:
// - act_as cookie (if set)
// - X-Principal header
// - X-User header
// - anonymous
func PrincipalFromRequest(r *http.Request) string {
    if c, err := r.Cookie("act_as"); err == nil && c.Value != "" {
        return c.Value
    }
    if v := r.Header.Get("X-Principal"); v != "" {
        return v
    }
    if v := r.Header.Get("X-User"); v != "" {
        return v
    }
    return "user:anonymous"
}

// Can checks authorization using the provided client and request context.
func Can(ctx context.Context, c Client, r *http.Request, object, relation string) (bool, error) {
    principal := PrincipalFromRequest(r)
    allowed, err := c.Check(ctx, principal, object, relation)
    if err != nil {
        // Be explicit in logs; do not allow on error by default.
        log.Printf("authz check error user=%s object=%s relation=%s: %v", principal, object, relation, err)
        return false, err
    }
    return allowed, nil
}

