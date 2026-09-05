# Visual verification on this box (no Go, no DB, no root)

1. Mock API (fixtures for /me, workspaces, agent runs, run detail, events):
   `python3 .impeccable/review/mock-api.py 18080 &`
2. Dev server: `npx vite --host 0.0.0.0 --port 5173 &` (proxies /api to 18080)
3. Headless Chromium needs shared libs that are not installed system-wide; they are
   extracted under /tmp/chromedeps/root (libatk, libatk-bridge, libatspi, libXdamage,
   libxkbcommon, libasound, libgbm, libwayland-server; 22.04 .debs from mirrors.zju.edu.cn,
   fetched with `apt-get -o Acquire::http::Proxy=http://127.0.0.1:17892 download …`).
   CJK glyphs come from ~/.fonts/NotoSansCJKsc-*.otf (fc-cache -f).
4. Capture: `LD_LIBRARY_PATH=/tmp/chromedeps/root/usr/lib/x86_64-linux-gnu node .impeccable/review/shoot.mjs`
