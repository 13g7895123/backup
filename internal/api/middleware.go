package api

import (
	"net/http"
	"os"
	"strings"
)

// CORSMiddleware 讀取環境變數 CORS_ORIGINS（逗號分隔）作為允許的來源清單，
// 動態比對每個請求的 Origin header，符合才回傳對應的 CORS headers。
func CORSMiddleware(next http.Handler) http.Handler {
	raw := os.Getenv("CORS_ORIGINS")
	allowed := make(map[string]bool)
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Agent-Code, X-Agent-Token")
			w.Header().Set("Vary", "Origin")
		}

		// OPTIONS preflight 請求直接回應，不繼續往下執行
		// 若來源不在允許清單，沒有 CORS headers，回 403 讓瀏覽器明確知道被拒絕
		if r.Method == http.MethodOptions {
			if allowed[origin] {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusForbidden)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}
