package tvbox

import (
	"encoding/xml"
	"strings"
)

type xmlClass struct {
	ID   string `xml:"id,attr"`
	Name string `xml:",chardata"`
}

type xmlDD struct {
	Flag string `xml:"flag,attr"`
	Body string `xml:",chardata"`
}

type xmlVideo struct {
	ID       string  `xml:"id"`
	Name     string  `xml:"name"`
	Pic      string  `xml:"pic"`
	Note     string  `xml:"note"`
	Year     string  `xml:"year"`
	Area     string  `xml:"area"`
	Actor    string  `xml:"actor"`
	Director string  `xml:"director"`
	Des      string  `xml:"des"`
	TypeName string  `xml:"type"`
	DDs      []xmlDD `xml:"dl>dd"`
}

type xmlList struct {
	Page        string     `xml:"page,attr"`
	PageCount   string     `xml:"pagecount,attr"`
	PageSize    string     `xml:"pagesize,attr"`
	RecordCount string     `xml:"recordcount,attr"`
	Videos      []xmlVideo `xml:"video"`
}

type xmlDoc struct {
	Classes []xmlClass `xml:"class>ty"`
	List    xmlList    `xml:"list"`
}

func parseCMSXML(body []byte) (cmsDoc, error) {
	var doc xmlDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return cmsDoc{}, err
	}
	out := cmsDoc{
		page: atoi(doc.List.Page), pages: atoi(doc.List.PageCount),
		limit: atoi(doc.List.PageSize), total: atoi(doc.List.RecordCount),
	}
	for _, c := range doc.Classes {
		id := strings.TrimSpace(c.ID)
		name := strings.TrimSpace(c.Name)
		if id == "" || name == "" {
			continue
		}
		out.classes = append(out.classes, map[string]any{"type_id": id, "type_name": name})
	}
	for _, v := range doc.List.Videos {
		id := strings.TrimSpace(v.ID)
		name := strings.TrimSpace(v.Name)
		if id == "" || name == "" {
			continue
		}
		from, urls := xmlPlay(v.DDs)
		vod := map[string]any{
			"vod_id": id, "vod_name": name, "vod_pic": strings.TrimSpace(v.Pic),
			"vod_remarks": strings.TrimSpace(v.Note), "vod_year": strings.TrimSpace(v.Year),
			"vod_area": strings.TrimSpace(v.Area), "vod_actor": strings.TrimSpace(v.Actor),
			"vod_director": strings.TrimSpace(v.Director), "vod_content": strings.TrimSpace(v.Des),
			"type_name": strings.TrimSpace(v.TypeName),
		}
		if from != "" {
			vod["vod_play_from"] = from
			vod["vod_play_url"] = urls
		}
		out.list = append(out.list, vod)
	}
	return out, nil
}

func xmlPlay(dds []xmlDD) (string, string) {
	var flags, groups []string
	for i, dd := range dds {
		body := strings.TrimSpace(dd.Body)
		if body == "" {
			continue
		}
		flag := strings.TrimSpace(dd.Flag)
		if flag == "" {
			flag = "线路" + itoa(i+1)
		}
		flags = append(flags, flag)
		groups = append(groups, body)
	}
	return strings.Join(flags, "$$$"), strings.Join(groups, "$$$")
}

func atoi(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
