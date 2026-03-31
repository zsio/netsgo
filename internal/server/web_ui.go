package server

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if s.webFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, devModeHTML)
		return
	}

	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	filePath := strings.TrimPrefix(path, "/")
	f, err := s.webFS.Open(filePath)
	if err == nil {
		f.Close()
		s.webHandler.ServeHTTP(w, r)
		return
	}

	indexFile, err := s.webFS.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer indexFile.Close()

	stat, err := indexFile.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	rs, ok := indexFile.(readSeeker)
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "index.html", stat.ModTime(), rs)
}

type readSeeker interface {
	Read(p []byte) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
}

const devModeHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NetsGo — 开发模式</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            background: linear-gradient(135deg, #0f0c29, #302b63, #24243e);
            color: #fff; min-height: 100vh;
            display: flex; align-items: center; justify-content: center;
        }
        .container {
            text-align: center; padding: 2rem;
            background: rgba(255,255,255,0.05);
            border-radius: 16px; backdrop-filter: blur(10px);
            border: 1px solid rgba(255,255,255,0.1);
            max-width: 520px;
        }
        h1 { font-size: 2.5rem; margin-bottom: 0.5rem; }
        h1 span { color: #7c3aed; }
        p { color: #a0a0b0; font-size: 1.1rem; margin: 0.5rem 0; }
        .badge {
            display: inline-block; margin-top: 1rem; padding: 0.4rem 1rem;
            background: #7c3aed; border-radius: 20px; font-size: 0.85rem;
        }
        code {
            display: block; margin-top: 1rem; padding: 0.8rem 1.2rem;
            background: rgba(255,255,255,0.08); border-radius: 8px;
            font-family: 'JetBrains Mono', 'Fira Code', monospace;
            font-size: 0.9rem; color: #c4b5fd; text-align: left;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 <span>NetsGo</span></h1>
        <p>服务端已启动 — 开发模式</p>
        <p>前端资源未嵌入，请独立启动 Vite 开发服务器：</p>
        <code>cd web && bun run dev</code>
        <p>然后访问 Vite 管理面板地址（默认 <a href="http://localhost:5173" style="color:#a78bfa">localhost:5173</a>）。</p>
        <div class="badge">Dev Mode 🔧</div>
    </div>
</body>
</html>`
