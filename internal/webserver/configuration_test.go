package webserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/app"
	"github.com/freelxs/jstv-player/internal/catvod"
)

const configurationBrowserPage = `<!doctype html><html><head></head><body><script>(async()=>{
 try{
  localStorage.setItem('theme','dark');
  const before=await (await fetch('/website/live')).json();
  await fetch('/website/live',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({sources:[{name:'fixture',url:'https://example.com/live.m3u'}]})});
  const after=await (await fetch('/website/live')).json();
  let blocked=false;try{top.document.body.getAttribute('id')}catch(e){blocked=true}
  let rpcBlocked=false;try{const r=await fetch('/api/call',{method:'POST',credentials:'include',headers:{'Content-Type':'application/json','X-JSTV-Request':'1'},body:JSON.stringify({method:'GetSettings'})});rpcBlocked=!r.ok}catch(e){rpcBlocked=true}
  top.postMessage({type:'fixture-result',ok:after.data.sources[0].name==='fixture',blocked,rpcBlocked,cookie:after.cookie,storage:localStorage.getItem('theme')},'*');
 }catch(e){top.postMessage({type:'fixture-result',error:String(e)},'*')}
})();
</script></body></html>`

const configurationFixture = `let server;let sources=[];
exports.start=()=>{server=catServerFactory(async(req,res)=>{
 const p=req.url.split('?')[0];
 const send=o=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(o))};
 if(p==='/config')return send({video:{sites:[{key:'config',name:'配置',type:3,api:'/mock'}]}});
 if(p==='/mock/init')return send({});
 if(p==='/website/live'){
  if(req.method==='POST'){let text='';for await(const b of req)text+=b; sources=JSON.parse(text).sources;}
  return send({code:0,data:{sources},cookie:req.headers.cookie||'',authorization:req.headers.authorization||''});
 }
 if(p==='/website/index.html'){
  res.setHeader('Content-Type','text/html');res.end(CONFIG_HTML);return;
 }
 res.statusCode=404;res.end('not found');
});server.listen(0)};exports.stop=()=>server&&server.close();`

func configurationTestRuntime(t *testing.T) (*app.Runtime, *authStore, string) {
	t.Helper()
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	// The JavaScript template is assembled without evaluating any remote source.
	script := strings.Replace(configurationFixture, "CONFIG_HTML", strconv.Quote(configurationBrowserPage), 1)
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js" {
			fmt.Fprint(w, script)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(feed.Close)
	dir := t.TempDir()
	runtime := app.New(dir, os.DirFS("../.."), true)
	t.Cleanup(runtime.Player.StopSource)
	auth, err := openAuth(dir, "admin", "fixture-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Player.AddSubscription("fixture", feed.URL+"/index.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Player.LoadSource(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Player.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Player.LoadSource(); err != nil {
		t.Fatal(err)
	}
	token, err := auth.login("127.0.0.1", "admin", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	return runtime, auth, token
}

func TestConfigurationCapabilitiesScopeRoutesAndRevoke(t *testing.T) {
	runtime, auth, token := configurationTestRuntime(t)
	if page, err := runtime.Player.OpenConfigPage("/unavailable-site"); err != nil || page != "/configuration/" {
		t.Fatalf("configuration depends on broken spider init: %q %v", page, err)
	}
	server := httptest.NewServer(newWebHandler(runtime, auth, os.DirFS("../..")))
	defer server.Close()
	req, _ := http.NewRequest("GET", server.URL+"/configuration/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `sandbox="allow-scripts allow-forms allow-downloads allow-popups"`) {
		t.Fatalf("missing isolation: %s", body)
	}
	match := regexp.MustCompile(`src="(/plugin-configuration/[^" ]+)/website/index.html"`).FindStringSubmatch(string(body))
	if len(match) != 2 {
		t.Fatalf("missing scoped URL: %s", body)
	}
	prefix := match[1]
	for _, tc := range []struct {
		route  string
		status int
	}{{"/website/live", 200}, {"/website/playground/run", 403}, {"/website/database/info", 403}, {"/config", 403}, {"/website/assets/../playground/env", 403}} {
		resp, err := http.Get(server.URL + prefix + tc.route)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: %d %s", tc.route, resp.StatusCode, data)
		}
		if tc.route == "/website/live" && strings.Contains(string(data), token) {
			t.Fatal("session cookie forwarded")
		}
	}
	runtime.Player.StopSource()
	resp, err = http.Get(server.URL + prefix + "/website/live")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("old configuration capability survived stop")
	}
}

func TestConfigurationBrowserReadWriteAndIsolation(t *testing.T) {
	chrome := os.Getenv("CHROME")

	if chrome == "" {
		t.Skip("set CHROME to run real configuration browser test")
	}
	runtime, auth, token := configurationTestRuntime(t)
	appHandler := newWebHandler(runtime, auth, os.DirFS("../.."))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/harness" {
			// Exercise the real wrapper as the top document; production deliberately
			// prevents embedding this wrapper inside another page.
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			r.URL.Path = "/configuration/"
			recorder := httptest.NewRecorder()
			appHandler.ServeHTTP(recorder, r)
			for key, values := range recorder.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			auth.setCookie(w, token)
			w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
			observer := `<pre id="result">pending</pre><script>window.addEventListener('message',e=>{if(e.data&&e.data.type==='fixture-result')document.getElementById('result').textContent=JSON.stringify({...e.data,origin:e.origin})})</script>`
			body := strings.Replace(recorder.Body.String(), "<iframe", observer+"<iframe", 1)
			fmt.Fprint(w, body)
			return
		}
		appHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run", "--disable-background-networking", "--disable-dev-shm-usage", "--user-data-dir="+filepath.Join(t.TempDir(), "browser"), "--virtual-time-budget=6000", "--dump-dom", server.URL+"/harness")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v %s", err, out)
	}
	for _, expected := range []string{`"ok":true`, `"blocked":true`, `"rpcBlocked":true`, `"cookie":""`, `"storage":"dark"`, `"origin":"null"`} {
		if !strings.Contains(string(out), expected) {
			t.Fatalf("browser evidence missing %s: %s", expected, out)
		}
	}
}
