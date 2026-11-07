package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/atomicfile"
)

type Favorite struct {
	Subscription string `json:"subscription"`
	API          string `json:"api"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Picture      string `json:"picture,omitempty"`
	Time         int64  `json:"time"`
}

// Persistent identities exclude the in-memory native source generation.
func persistentAPI(api string) string {
	if strings.HasPrefix(api, "live:") {
		return "live"
	}
	return api
}

func (p *Player) readFavorites() (map[string]Favorite, error) {
	favorites := map[string]Favorite{}
	b, err := os.ReadFile(filepath.Join(p.dir, "favorites.json"))
	if os.IsNotExist(err) {
		return favorites, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &favorites); err != nil || favorites == nil {
		return nil, errors.New("收藏文件损坏，原文件已保留")
	}
	return favorites, nil
}

func (p *Player) GetFavorites() ([]Favorite, error) {
	subscription := p.activeSubID()
	p.mu.Lock()
	liveAPI := p.liveAPI
	p.mu.Unlock()
	p.libraryMu.Lock()
	defer p.libraryMu.Unlock()
	favorites, err := p.readFavorites()
	if err != nil {
		return nil, err
	}
	out := []Favorite{}
	for _, item := range favorites {
		if item.Subscription == subscription {
			if item.API == "live" {
				item.API = liveAPI
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out, nil
}

func (p *Player) ToggleFavorite(api, id, name, picture string) (bool, error) {
	if id == "" || name == "" || len(id) > 8192 || len(name) > 512 || len(picture) > 8192 {
		return false, errors.New("收藏信息无效")
	}
	subscription := p.activeSubID()
	if subscription == "" {
		return false, errNoSub
	}
	p.mu.Lock()
	valid := false
	for _, site := range p.sites {
		if site.API == api {
			valid = true
			break
		}
	}
	p.mu.Unlock()
	if !valid {
		return false, errSourceChanged
	}
	api = persistentAPI(api)
	p.libraryMu.Lock()
	defer p.libraryMu.Unlock()
	favorites, err := p.readFavorites()
	if err != nil {
		return false, err
	}
	key := historyKey(subscription, api, id)
	added := false
	if _, ok := favorites[key]; ok {
		delete(favorites, key)
	} else {
		if len(favorites) >= 2000 {
			return false, errors.New("收藏已达 2000 项，请先删除一些收藏")
		}
		favorites[key] = Favorite{Subscription: subscription, API: api, ID: id, Name: name, Picture: picture, Time: time.Now().Unix()}
		added = true
	}
	if err := atomicfile.JSON(filepath.Join(p.dir, "favorites.json"), favorites); err != nil {
		return false, err
	}
	return added, nil
}
