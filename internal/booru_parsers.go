package internal

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"strconv"
	"strings"
)

// flexBool принимает JSON-булево, строку "true"/"false", "1"/"0" и числа:
// dapi отдаёт has_comments строкой даже при json=1, у части сайтов поле
// отсутствует (тогда остаётся false).
type flexBool bool

func (fb *flexBool) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes":
			*fb = true
		}
		return nil
	}
	var t bool
	if err := json.Unmarshal(b, &t); err == nil {
		*fb = flexBool(t)
		return nil
	}
	var n flexInt
	if err := n.UnmarshalJSON(b); err == nil {
		*fb = n != 0
	}
	return nil
}

// SourceComment — структура элемента <comment> в XML-ответе dapi. Атрибуты
// приходят строками (в том числе время в формате сайта, а не RFC3339), поэтому
// разбираем всё через string и приводим сами.
type sourceCommentXML struct {
	ID        string `xml:"id,attr"`
	PostID    string `xml:"post_id,attr"`
	Body      string `xml:"body,attr"`
	Text      string `xml:"text,attr"`
	Comment   string `xml:"comment,attr"`
	Creator   string `xml:"creator,attr"`
	Author    string `xml:"author,attr"`
	Username  string `xml:"username,attr"`
	CreatedAt string `xml:"created_at,attr"`
}

func (s sourceCommentXML) toSourceComment() SourceComment {
	body := s.Body
	if body == "" {
		body = s.Text
	}
	if body == "" {
		body = s.Comment
	}
	author := s.Creator
	if author == "" {
		author = s.Author
	}
	if author == "" {
		author = s.Username
	}
	id, _ := strconv.Atoi(strings.TrimSpace(s.ID))
	postID, _ := strconv.Atoi(strings.TrimSpace(s.PostID))
	return SourceComment{
		ID:        id,
		PostID:    postID,
		Author:    strings.TrimSpace(author),
		Body:      strings.TrimSpace(body),
		CreatedAt: strings.TrimSpace(s.CreatedAt),
	}
}

// parseDapiComments понимает обе формы ответа s=comment:
// XML <comments type="array"><comment …/></comments> (hypnohub, safebooru
// игнорируют json=1) и JSON — голый массив либо обёртку {"comment":[…]}.
func parseDapiComments(body []byte) ([]SourceComment, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return []SourceComment{}, nil
	}

	if trimmed[0] == '<' {
		var doc struct {
			Items []sourceCommentXML `xml:"comment"`
		}
		if err := xml.Unmarshal(trimmed, &doc); err != nil {
			return nil, fmt.Errorf("failed to parse comments XML: %w", err)
		}
		out := make([]SourceComment, 0, len(doc.Items))
		for _, it := range doc.Items {
			c := it.toSourceComment()
			// Запись без текста (а у hypnohub body может быть пустым) в
			// список не попадает — иначе в UI будет пустая строка.
			if c.Body == "" {
				continue
			}
			out = append(out, c)
		}
		return out, nil
	}

	if trimmed[0] != '{' && trimmed[0] != '[' {
		// Не JSON и не XML: gelbooru на s=comment отвечает 200 с текстом
		// «Disabled due to abuse.» — сервис отключён их стороной.
		msg := strings.TrimSpace(string(trimmed))
		if len(msg) > 120 {
			msg = msg[:120]
		}
		return nil, fmt.Errorf("%w: %s", ErrSourceCommentsUnsupported, msg)
	}

	var raws []json.RawMessage
	if trimmed[0] == '{' {
		var wrap struct {
			Comment  []json.RawMessage `json:"comment"`
			Comments []json.RawMessage `json:"comments"`
		}
		if err := json.Unmarshal(trimmed, &wrap); err != nil {
			return nil, err
		}
		if wrap.Comment != nil {
			raws = wrap.Comment
		} else {
			raws = wrap.Comments
		}
	} else if err := json.Unmarshal(trimmed, &raws); err != nil {
		return nil, err
	}

	out := make([]SourceComment, 0, len(raws))
	for _, raw := range raws {
		var w struct {
			ID        flexInt `json:"id"`
			PostID    flexInt `json:"post_id"`
			Body      string  `json:"body"`
			Text      string  `json:"text"`
			Comment   string  `json:"comment"`
			Creator   string  `json:"creator"`
			Author    string  `json:"author"`
			Username  string  `json:"username"`
			CreatedAt string  `json:"created_at"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			log.Printf("[dapi] пропущен битый комментарий: %v", err)
			continue
		}
		body := w.Body
		if body == "" {
			body = w.Text
		}
		if body == "" {
			body = w.Comment
		}
		author := w.Creator
		if author == "" {
			author = w.Author
		}
		if author == "" {
			author = w.Username
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		out = append(out, SourceComment{
			ID:        int(w.ID),
			PostID:    int(w.PostID),
			Author:    strings.TrimSpace(author),
			Body:      body,
			CreatedAt: strings.TrimSpace(w.CreatedAt),
		})
	}
	return out, nil
}

// SourceComments тянет комментарии поста у сайта (dapi s=comment). Один запрос,
// без ретраев: вызывается вручную из UI, а комментарии источника иммутабельны
// и кэшируются на стороне БД (см. handlers_sourcecomments.go).

func parseDapiPosts(body []byte) ([]Rule34Post, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return []Rule34Post{}, nil
	}

	var raws []json.RawMessage
	if trimmed[0] == '{' {
		var wrap struct {
			Post []json.RawMessage `json:"post"`
		}
		if err := json.Unmarshal(trimmed, &wrap); err != nil {
			return nil, err
		}
		raws = wrap.Post
	} else {
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, err
		}
	}

	out := make([]Rule34Post, 0, len(raws))
	for _, raw := range raws {
		p, err := parseDapiPost(raw)
		if err != nil {
			log.Printf("[dapi] пропущен битый пост: %v", err)
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func parseDapiPost(raw []byte) (Rule34Post, error) {
	var w struct {
		ID         flexInt `json:"id"`
		Tags       string  `json:"tags"`
		FileURL    string  `json:"file_url"`
		SampleURL  string  `json:"sample_url"`
		PreviewURL string  `json:"preview_url"`
		Width      flexInt `json:"width"`
		Height     flexInt `json:"height"`
		Score      flexInt `json:"score"`
		Rating     string  `json:"rating"`
		Image      string  `json:"image"`
		Hash       string  `json:"hash"`
		Owner      string  `json:"owner"`
		Source     string  `json:"source"`
		Status     string  `json:"status"`
		FileSize   flexInt `json:"file_size"`
		// has_comments приходит строкой "true"/"false" даже при json=1.
		HasComments flexBool `json:"has_comments"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return Rule34Post{}, err
	}
	if w.ID <= 0 {
		return Rule34Post{}, fmt.Errorf("post without valid id")
	}
	// Ghost-записи (safebooru при -rating:-метатегах): id есть, но
	// directory/image null и file_url собран мусорным — "…/images//".
	// Минимальные записи без image/tags проект сознательно терпит
	// (см. TestDapiParsingTypeTolerance), поэтому бракуем только URL,
	// заканчивающийся слэшем: у настоящего файла после него всегда имя.
	if strings.HasSuffix(w.FileURL, "/") {
		return Rule34Post{}, fmt.Errorf("post %d with malformed file_url", int(w.ID))
	}
	return Rule34Post{
		ID:          int(w.ID),
		Tags:        w.Tags,
		FileURL:     w.FileURL,
		SampleURL:   w.SampleURL,
		PreviewURL:  w.PreviewURL,
		Width:       int(w.Width),
		Height:      int(w.Height),
		Score:       int(w.Score),
		Rating:      w.Rating,
		Image:       w.Image,
		Hash:        w.Hash,
		Owner:       w.Owner,
		Source:      w.Source,
		Status:      w.Status,
		FileSize:    int(w.FileSize),
		FileType:    detectFileType(w.FileURL),
		HasComments: w.HasComments,
	}, nil
}

// flexInt принимает и число, и строку с числом ("123"), и мусор вроде
// "42\/9b" (в этом случае 0): сайты семейства непоследовательны в типах.
type flexInt int

func (fi *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return nil
		}
		n, _ := strconv.ParseInt(strings.Trim(strings.TrimSpace(s), `"\`), 10, 64)
		*fi = flexInt(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return nil
	}
	*fi = flexInt(n)
	return nil
}
