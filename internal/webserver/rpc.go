package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/freelxs/jstv-player/internal/app"
)

type rpcRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

func (s *webServer) handleCall(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var req rpcRequest
	if err := decodeJSON(w, r, 4<<20, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	name := strings.TrimPrefix(req.Method, "Player.")
	if name == "SearchAll" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "搜索全部站点请使用流式接口"})
		return
	}
	result, err := s.invoke(r.Context(), name, req.Args)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *webServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var req rpcRequest
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	if strings.TrimPrefix(req.Method, "Player.") != "SearchAll" || len(req.Args) < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未知的流式调用"})
		return
	}
	var wd string
	if err := json.Unmarshal(req.Args[0], &wd); err != nil || strings.TrimSpace(wd) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请输入搜索词"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "服务器不支持流式输出"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	enc := json.NewEncoder(w)
	err := s.runtime.StreamSearch(r.Context(), wd, func(result app.SiteResults) error {
		if err := enc.Encode(map[string]any{"type": "item", "data": result}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		_ = enc.Encode(map[string]any{"type": "error", "error": err.Error()})
		flusher.Flush()
		return
	}
	_ = enc.Encode(map[string]any{"type": "done"})
	flusher.Flush()
}

func (s *webServer) invoke(ctx context.Context, name string, args []json.RawMessage) (any, error) {
	m, ok := s.methods[name]
	if !ok {
		return nil, errors.New("未知接口")
	}
	mt := m.Type
	in := make([]reflect.Value, 0, mt.NumIn())
	in = append(in, reflect.ValueOf(s.player))
	argi := 0
	for i := 1; i < mt.NumIn(); i++ {
		pt := mt.In(i)
		if pt == reflect.TypeOf((*context.Context)(nil)).Elem() {
			if ctx == nil {
				ctx = context.Background()
			}
			in = append(in, reflect.ValueOf(ctx))
			continue
		}
		if argi >= len(args) {
			return nil, errors.New("参数不足")
		}
		v := reflect.New(pt)
		if err := json.Unmarshal(args[argi], v.Interface()); err != nil {
			return nil, errors.New("参数无效")
		}
		in = append(in, v.Elem())
		argi++
	}
	if argi != len(args) {
		return nil, errors.New("参数过多")
	}
	out := m.Func.Call(in)
	var result any
	for _, v := range out {
		if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			if !v.IsNil() {
				return nil, v.Interface().(error)
			}
			continue
		}
		if result != nil {
			return nil, errors.New("接口返回值无法编码")
		}
		if v.Kind() == reflect.Pointer && v.IsNil() {
			result = nil
			continue
		}
		result = v.Interface()
	}
	return result, nil
}

func playerMethods() map[string]reflect.Method {
	allow := map[string]bool{
		"Logs": true, "ClearLogs": true, "GetSettings": true, "SaveSettings": true,
		"AddSubscription": true, "UpdateSubscription": true, "RemoveSubscription": true,
		"SwitchSubscription": true, "LoadSource": true, "ApproveSource": true, "StopSource": true,
		"ProbeNodePermissions": true, "Home": true, "Category": true, "Search": true,
		"Detail": true, "GetHistory": true, "Open": true, "PlayEpisodes": true,
		"PickCustomPlayer": true, "WebPlay": true, "WebAdvance": true,
	}
	out := map[string]reflect.Method{}
	typ := reflect.TypeOf(&app.Player{})
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if allow[m.Name] {
			out[m.Name] = m
		}
	}
	return out
}
