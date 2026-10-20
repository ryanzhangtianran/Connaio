# Serves frontend/dist with a fake Wails backend: every bound method answers with demo data, so the
# real UI renders for README screenshots without anyone's live traffic.
# usage: build/preview/render.sh (starts this, then shoots docs/preview_light.png and preview_dark.png)
import base64, datetime, json, os, re, subprocess, sys, tempfile
from http.server import ThreadingHTTPServer, SimpleHTTPRequestHandler

repo, port = sys.argv[1], int(sys.argv[2])
dist = os.path.join(repo, "frontend", "dist")
bind = os.path.join(repo, "frontend", "bindings", "github.com", "ryanzhangtianran", "Connaio", "internal", "app")

methods = {}  # id -> "CoreService.Activity"
for f in os.listdir(bind):
    if f.endswith("service.ts"):
        svc = {"coreservice": "CoreService", "deviceservice": "DeviceService", "enhancedservice": "EnhancedService",
               "profileservice": "ProfileService", "settingsservice": "SettingsService",
               "systemproxyservice": "SystemProxyService", "toolservice": "ToolService"}[f[:-3]]
        src = open(os.path.join(bind, f)).read()
        for name, mid in re.findall(r"export function (\w+)\([^)]*\)[^{]*\{\s*return \$Call\.ByID\((\d+)", src):
            methods[int(mid)] = f"{svc}.{name}"

def icon(name):
    """The installed app's icon as a PNG data URL, "" when it isn't installed (the page draws a default)."""
    for d in ("/Applications", "/System/Applications"):
        app = f"{d}/{name}.app"
        if os.path.isdir(app):
            break
    else:
        return ""
    res = os.path.join(app, "Contents", "Resources")
    f = subprocess.run(["defaults", "read", app + "/Contents/Info.plist", "CFBundleIconFile"], capture_output=True, text=True).stdout.strip()
    src = os.path.join(res, f if f.endswith(".icns") else f + ".icns")
    if not os.path.exists(src):
        src = next((os.path.join(res, x) for x in sorted(os.listdir(res)) if x.endswith(".icns")), "")
    out = os.path.join(tempfile.gettempdir(), "connaio-preview-icon.png")
    if not src or subprocess.run(["sips", "-s", "format", "png", "-z", "64", "64", src, "--out", out], capture_output=True).returncode:
        return ""
    with open(out, "rb") as fh:
        return "data:image/png;base64," + base64.b64encode(fh.read()).decode()

GB, MB = 1_000_000_000, 1_000_000
now = datetime.datetime.now()
hours = []
for h in range(24):
    if h > now.hour:
        hours.append({"direct": 0, "proxy": 0})
    else:
        w = [0, 0, 0, 0, 0, 0, 0, 0.2, 0.6, 1.4, 2.1, 1.8, 0.9, 1.2, 2.6, 3.1, 2.4, 1.7, 1.1, 2.2, 2.9, 2.5, 1.6, 0.7][h]
        hours.append({"direct": int(w * 180 * MB), "proxy": int(w * 120 * MB)})

apps = [("Google Chrome", 1.42, 0.61), ("WeChat", 0.38, 0.02), ("Visual Studio Code", 0.05, 0.29), ("Ghostty", 0.04, 0.22),
        ("ChatGPT", 0.0, 0.18), ("zoom.us", 0.16, 0.0), ("Obsidian", 0.01, 0.04), ("System Settings", 0.03, 0.0)]
by_process = [{"name": "macOS" if n == "System Settings" else ("Zoom" if n == "zoom.us" else n), "icon": f"/Applications/{n}.app",
               "direct": int(d * GB), "proxy": int(p * GB)} for n, d, p in apps]
icons = {f"/Applications/{n}.app": icon(n) for n, _, _ in apps}
by_process.insert(2, {"name": "隧道 vps", "icon": "tunnel", "direct": 0, "proxy": int(0.31 * GB)})  # a reverse tunnel's users

activity = {
    "today": {"direct": int(2.13 * GB), "proxy": int(1.36 * GB)}, "month": {"direct": int(41.8 * GB), "proxy": int(27.5 * GB)},
    "network": "Wi-Fi · Home", "mode": "rule", "tun": False,
    "connections": 86, "processes": 17, "devices": 1, "hosts": 54, "hours": hours,
    "byProcess": by_process,
    "byHost": [{"name": h, "direct": int(d * GB), "proxy": int(p * GB)} for h, d, p in
               [("googlevideo.com", 0, 0.52), ("github.com", 0, 0.21), ("weixin.qq.com", 0.33, 0), ("apple.com", 0.24, 0),
                ("openai.com", 0, 0.17), ("bilibili.com", 0.71, 0), ("zoom.us", 0.16, 0), ("npmjs.org", 0, 0.08)]],
    "days": [{"direct": int(abs(((d * 37) % 11) - 3) * 0.4 * GB), "proxy": int(((d * 53) % 7) * 0.3 * GB)} if d < now.day else {"direct": 0, "proxy": 0}
             for d in range(31)],
    "monthByProcess": [dict(u, direct=u["direct"] * 14, proxy=u["proxy"] * 14) for u in by_process],
    "byPolicy": [{"name": h, "direct": int(d * GB), "proxy": int(p * GB)} for h, d, p in
                 [("DIRECT", 2.13, 0), ("香港 01", 0, 0.74), ("日本 02", 0, 0.41), ("新加坡 01", 0, 0.21)]],
}
groups = [{"name": "Proxy", "type": "Selector", "now": "香港 01", "all": [
    {"name": "香港 01", "type": "Vmess", "delay": 62}, {"name": "日本 02", "type": "Vmess", "delay": 88},
    {"name": "新加坡 01", "type": "Trojan", "delay": 104}, {"name": "美国 03", "type": "Trojan", "delay": 186}]}]
groups += [dict(groups[0], name=n, now=m) for n, m in [("AI", "新加坡 01"), ("Streaming", "日本 02"), ("Telegram", "香港 01"),
                                                      ("Apple", "美国 03"), ("Microsoft", "香港 01"), ("Games", "日本 02")]]
start = (now - datetime.timedelta(minutes=3)).astimezone().isoformat()
conns = [{"id": str(i), "network": "tcp", "inbound": "HTTP", "process": p, "path": f"/Applications/{a}.app/Contents/MacOS/{p}", "app": a,
          "source": "127.0.0.1", "host": h, "port": "443", "rule": r, "chain": c, "upload": 20_000 * i, "download": 900_000 * i, "start": start}
         for i, (a, p, h, r, c) in enumerate([
             ("Google Chrome", "Google Chrome Helper", "www.youtube.com", "DomainSuffix(youtube.com)", "Proxy → 香港 01"),
             ("Google Chrome", "Google Chrome Helper", "rr3---sn.googlevideo.com", "DomainSuffix(googlevideo.com)", "Proxy → 香港 01"),
             ("Google Chrome", "Google Chrome Helper", "github.com", "DomainSuffix(github.com)", "Proxy → 香港 01"),
             ("Google Chrome", "Google Chrome Helper", "www.netflix.com", "GeoSite(netflix)", "Streaming → 日本 02"),
             ("Google Chrome", "Google Chrome Helper", "chatgpt.com", "DomainSuffix(chatgpt.com)", "AI → 新加坡 01"),
             ("Google Chrome", "Google Chrome Helper", "www.bilibili.com", "GeoSite(cn)", "DIRECT"),
             ("Google Chrome", "Google Chrome Helper", "s1.hdslb.com", "GeoSite(cn)", "DIRECT"),
             ("WeChat", "WeChat", "weixin.qq.com", "GeoSite(cn)", "DIRECT"),
             ("Ghostty", "node", "registry.npmjs.org", "Match", "Proxy → 日本 02"),
         ], 1)]
# a request a reverse tunnel brought to the proxy port, with the tunnel's hop (linkTunnels)
conns.insert(0, {"id": "t1", "network": "tcp", "inbound": "Mixed", "process": "", "path": "", "app": "隧道 vps", "icon": "tunnel",
                 "source": "127.0.0.1", "host": "api.openai.com", "port": "443", "rule": "DomainSuffix(openai.com)",
                 "chain": "AI → 新加坡 01", "upload": 310_000, "download": 4_200_000, "start": start, "inName": "vps",
                 "tunnel": {"name": "vps", "type": "SSH", "from": "198.51.100.77:51544", "target": "127.0.0.1:7890"}})
tunnels = [{"name": "vps", "type": "ssh", "state": "running", "error": "", "since": start,
            "forwards": [{"remote": "0.0.0.0:17890", "local": "127.0.0.1:7890"}]}]
sites = [{"name": "Google", "url": "https://www.google.com/generate_204"}, {"name": "GitHub", "url": "https://github.com"},
         {"name": "Claude", "url": "https://claude.ai"}, {"name": "ChatGPT", "url": "https://chatgpt.com"},
         {"name": "Bilibili", "url": "https://www.bilibili.com"}]  # one of the user's own: its initial, no icon offline
exit_ips = {
    "DIRECT": {"ip": "198.51.100.23", "code": "CN", "country": "China", "region": "Shanghai", "city": "Shanghai", "isp": "China Telecom"},
    "香港 01": {"ip": "203.0.113.45", "code": "HK", "country": "Hong Kong", "region": "", "city": "Hong Kong", "isp": "Example Networks Ltd"},
    "新加坡 01": {"ip": "203.0.113.102", "code": "SG", "country": "Singapore", "region": "", "city": "Singapore", "isp": "Example Hosting Pte"},
    "日本 02": {"ip": "203.0.113.78", "code": "JP", "country": "Japan", "region": "Tokyo", "city": "Tokyo", "isp": "Example Cloud KK"},
}

def answer(name, args):
    if name == "ToolService.ExitIP":
        return exit_ips.get(args[0], exit_ips["香港 01"])
    if name == "SettingsService.AppIcon":
        return icons.get(args[0], "")
    return {
        "CoreService.Status": {"running": True, "version": "1.19.32", "error": ""},
        "CoreService.Activity": activity,
        "CoreService.Latency": {"internet": 41, "router": 3, "dns": 12, "proxy": 62, "group": "Proxy"},
        "CoreService.ExternalIP": "198.51.100.23",
        "CoreService.Groups": groups,
        "CoreService.Connections": conns,
        "CoreService.Events": [],
        "SettingsService.Sites": sites,
        "CoreService.Services": [dict(x, ms=m) for x, m in zip(sites, [48, 132, 210, 186, 35])],
        "CoreService.Recent": {"logs": [], "traffic": [], "session": {"up": 0, "down": 0}},
        "CoreService.ReverseTunnels": tunnels,
        "ProfileService.UserItems": {"tunnels": [{"name": "vps", "type": "ssh", "server": "vps.example.com", "port": 22}]},
        "CoreService.Mode": "rule",
        "SystemProxyService.Enabled": True,
        "EnhancedService.Mode": "process-tun",
        "EnhancedService.DaemonInstalled": True,
        "ProfileService.Profiles": {"current": "demo", "profiles": [{"id": "demo", "name": "Demo Cloud", "url": "https://example.com/sub",
                                    "updatedAt": start, "interval": 24}]},
        "ToolService.RepairChecks": [
            {"id": "proxy", "title": "残留系统代理", "detail": "无", "fix": "清除", "found": False},
            {"id": "cores", "title": "残留内核", "detail": "无", "fix": "结束", "found": False},
            {"id": "dns", "title": "DNS 缓存", "detail": "可刷新", "fix": "刷新", "found": False}],
    }.get(name)

# injected before the app: the platform the runtime reads, then a minute of traffic for the sparklines
inject = """<style>body::before{content:"";position:fixed;z-index:9;left:21px;top:19px;width:12px;height:12px;border-radius:50%;
background:#ff5f57;box-shadow:20px 0 #febc2e,40px 0 #28c840}</style>
<script>window._wails={environment:{OS:"darwin",Arch:"arm64",Debug:false}};</script>
<script>window.addEventListener("load",()=>setTimeout(()=>{for(let i=0;i<60;i++){const t=i/60;
window._wails.dispatchWailsEvent({name:"traffic",data:{up:Math.round(90e3+70e3*Math.sin(t*9)+40e3*Math.sin(t*23)),
down:Math.round(1.6e6+1.1e6*Math.sin(t*5)+0.6e6*Math.sin(t*17)+(i>40?0.9e6:0))}})}},800))</script>"""

class H(SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=dist, **k)

    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path in ("/", "/index.html"):
            html = open(os.path.join(dist, "index.html")).read().replace("<head>", "<head>" + inject, 1).encode()
            self.send_response(200); self.send_header("Content-Type", "text/html"); self.end_headers(); self.wfile.write(html)
            return
        super().do_GET()

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])) or b"{}")
        a = body.get("args") or {}
        name = methods.get(a.get("methodID"), "") if isinstance(a, dict) else ""
        out = json.dumps(answer(name, a.get("args") or []) if name else None).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.end_headers(); self.wfile.write(out)

ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
