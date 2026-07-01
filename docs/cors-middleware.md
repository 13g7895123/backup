# CORS Middleware 實作說明

## 什麼是 CORS？

CORS（Cross-Origin Resource Sharing，跨來源資源共用）是瀏覽器的安全機制。  
當前端網頁的**域名 / 埠 / 協定**與後端 API 不同時，瀏覽器會先發一個 **preflight 請求（OPTIONS）**詢問伺服器是否允許跨域存取，伺服器必須回應正確的 HTTP headers，瀏覽器才會放行真正的請求。

> **注意**：CORS 是瀏覽器行為，後端直接呼叫（curl、Postman、伺服器間通訊）完全不受影響。

---

## 改動了哪些檔案？

### 1. 新增 `internal/api/middleware.go`

```go
func CORSMiddleware(next http.Handler) http.Handler {
    // ... 見下方詳細說明
}
```

### 2. 修改 `cmd/dashboard/main.go`

```go
// 改前
Handler: mux,

// 改後
Handler: api.CORSMiddleware(mux),
```

---

## Go 語言知識點

### 知識點一：Middleware 模式（裝飾器模式）

Go 的 HTTP middleware 本質上是一個**接受 `http.Handler` 並回傳 `http.Handler`** 的函式：

```go
func CORSMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 前置邏輯（在 next 執行前）
        next.ServeHTTP(w, r)
        // 後置邏輯（在 next 執行後）
    })
}
```

這讓 middleware 可以「包裹」任意 handler，形成一條責任鏈（chain）：

```
Request → CORSMiddleware → mux → 實際 handler → Response
```

`http.Handler` 是一個 interface：

```go
type Handler interface {
    ServeHTTP(ResponseWriter, *Request)
}
```

而 `http.HandlerFunc` 是一個型別，讓普通函式可以實作這個 interface：

```go
type HandlerFunc func(ResponseWriter, *Request)

func (f HandlerFunc) ServeHTTP(w ResponseWriter, r *Request) {
    f(w, r)
}
```

所以 `http.HandlerFunc(func(...){...})` 的意思是：**把一個 func 轉型成 HandlerFunc，讓它同時也是 http.Handler**。

---

### 知識點二：閉包（Closure）與初始化時機

```go
func CORSMiddleware(next http.Handler) http.Handler {
    // ★ 這段在「伺服器啟動時」只執行一次
    raw := os.Getenv("CORS_ORIGINS")
    allowed := make(map[string]bool)
    for _, o := range strings.Split(raw, ",") {
        o = strings.TrimSpace(o)
        if o != "" {
            allowed[o] = true
        }
    }

    // ★ 這個 func 是「閉包」，每次請求進來才執行
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        origin := r.Header.Get("Origin")
        if allowed[origin] { // 直接使用外層的 allowed map
            // ...
        }
    })
}
```

`allowed` map 只在程式啟動時建立一次，之後每個請求共享同一份資料，這比每次請求都重新讀環境變數、重新 split 字串**效率更高**。

這就是 Go 閉包的特性：內層函式可以捕捉（capture）外層函式的變數，即使外層函式已經返回，那些變數仍然存活。

---

### 知識點三：`make(map[string]bool)` vs `map[string]bool{}`

```go
allowed := make(map[string]bool)  // 推薦
allowed := map[string]bool{}      // 等價，也常見
```

兩者功能相同，差異是 `make` 可以指定初始容量：

```go
make(map[string]bool, 10) // 預先分配 10 個 bucket，避免擴容
```

這裡選用 `make` 是 Go 慣例，語意上更明確：「我要建立一個 map」。

---

### 知識點四：`map[string]bool` 作為 Set

Go 沒有內建的 Set 型別，慣用做法是用 `map[string]bool` 或 `map[string]struct{}` 來模擬：

```go
// 查詢是否存在
if allowed[origin] { ... }

// 這等價於：
if val, ok := allowed[origin]; ok && val { ... }
```

當 key 不存在時，`map[string]bool` 的 zero value 是 `false`，所以 `allowed[origin]` 在 key 不存在時直接回傳 `false`，不會 panic，非常方便。

若要更省記憶體，可以用 `map[string]struct{}`（空結構體不佔記憶體）：

```go
allowed := map[string]struct{}{}
allowed["https://example.com"] = struct{}{}

_, ok := allowed[origin] // ok 為 true 代表存在
```

---

### 知識點五：`strings.Split` 與 `strings.TrimSpace`

```go
for _, o := range strings.Split(raw, ",") {
    o = strings.TrimSpace(o)
    if o != "" {
        allowed[o] = true
    }
}
```

- `strings.Split("a, b, c", ",")` → `["a", " b", " c"]`（注意空白）
- `strings.TrimSpace(" b")` → `"b"`（去掉前後空白）
- 最後 `if o != ""` 防止空字串被誤加進 map（例如 `CORS_ORIGINS=` 只有等號沒有值）

---

### 知識點六：`Vary: Origin` 為什麼重要？

```go
w.Header().Set("Vary", "Origin")
```

當不同 `Origin` 的請求打到同一個 URL，代理伺服器（Nginx、CDN）可能會快取第一個回應，然後把它交給所有後續請求，即使 Origin 不同。

加上 `Vary: Origin` 告訴代理：「這個回應依賴 Origin header，不同 Origin 要分開快取」。

這在同一個 API 服務多個前端域名時非常重要，否則 A 網站的 CORS 回應可能被快取後錯誤地回給 B 網站。

---

### 知識點七：OPTIONS Preflight 為什麼要提前 return？

```go
if r.Method == http.MethodOptions {
    w.WriteHeader(http.StatusNoContent)
    return
}
next.ServeHTTP(w, r)
```

瀏覽器在發真正請求前，會先發一個 `OPTIONS` 請求詢問：「我可以跨域存取嗎？允許哪些方法和 header？」

這個 preflight 請求**不需要執行任何業務邏輯**，只需要回應 CORS headers。  
如果不提前 `return`，請求會繼續往下傳給 `mux`，而 `mux` 沒有對應的 `OPTIONS` 路由，會回傳 `405 Method Not Allowed`，瀏覽器就會認為跨域被拒絕。

`http.StatusNoContent`（204）是 preflight 的標準回應碼，代表「允許，但沒有回傳內容」。

---

## 環境變數設定

在專案根目錄的 `.env` 檔案加入：

```env
# 多個來源用逗號分隔
CORS_ORIGINS=https://app.example.com,https://admin.example.com
```

---

## 部署步驟（正式環境 Docker）

```bash
# 1. 更新 .env 加入 CORS_ORIGINS
# 2. 重新 build image 並啟動
docker compose build dashboard && docker compose up -d dashboard
```

`postgres` 服務不受影響，無需重啟。
