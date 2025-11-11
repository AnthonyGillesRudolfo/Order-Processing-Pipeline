package authz

import (
    "net/http"
)

// Require returns a middleware that enforces an authz check.
// objectRel returns object and relation. If object is empty, the check is skipped.
func Require(c Client, objectRel func(*http.Request) (string, string)) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            obj, rel := objectRel(r)
            if obj == "" || rel == "" {
                next.ServeHTTP(w, r)
                return
            }
            allowed, err := Can(r.Context(), c, r, obj, rel)
            if err != nil {
                http.Error(w, "authorization error", http.StatusForbidden)
                return
            }
            if !allowed {
                http.Error(w, "forbidden", http.StatusForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

