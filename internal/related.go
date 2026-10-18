package internal

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// TagKind определяет категорию тега для взвешивания схожести
type TagKind string

const (
	TagKindArtist    TagKind = "artist"
	TagKindCharacter TagKind = "character"
	TagKindCopyright TagKind = "copyright"
	TagKindMeta      TagKind = "meta"
	TagKindGeneral   TagKind = "general"
)

var metaTagRe = regexp.MustCompile(`^(highres|absurdres|translated|commentary|pool:\d+|source:|bad_id|check_my_page|rating:|status:)`)

// ClassifyTag классифицирует тег по Booru-соглашениям
func ClassifyTag(tag string) TagKind {
	lower := strings.ToLower(tag)
	if strings.HasPrefix(lower, "artist:") || strings.HasSuffix(lower, "_(artist)") || strings.HasSuffix(lower, "_(style)") {
		return TagKindArtist
	}
	if strings.HasPrefix(lower, "copyright:") || strings.HasPrefix(lower, "series:") || strings.Contains(lower, "_(series)") {
		return TagKindCopyright
	}
	if strings.HasPrefix(lower, "character:") || strings.HasSuffix(lower, "_(cosplay)") ||
		strings.Contains(lower, "_(character)") || (strings.Contains(lower, "_(") && strings.HasSuffix(lower, ")")) {
		return TagKindCharacter
	}
	if strings.HasPrefix(lower, "meta:") || metaTagRe.MatchString(lower) || genericTags[lower] {
		return TagKindMeta
	}
	return TagKindGeneral
}

// CategorizedTag представляет классифицированный тег с весом
type CategorizedTag struct {
	Tag    string
	Kind   TagKind
	Weight float64
}

// AnalyzeTags анализирует строку тегов поста, отфильтровывает мусор и вычисляет веса
func AnalyzeTags(tagStr string, freqMap map[string]int) []CategorizedTag {
	fields := strings.Fields(tagStr)
	res := make([]CategorizedTag, 0, len(fields))
	seen := make(map[string]bool, len(fields))

	for _, raw := range fields {
		t := strings.ToLower(strings.TrimLeft(raw, "+-"))
		if len(t) < 3 || len(t) > 40 || seen[t] {
			continue
		}
		seen[t] = true
		kind := ClassifyTag(t)
		if kind == TagKindMeta {
			continue
		}

		baseWeight := 1.0
		switch kind {
		case TagKindArtist:
			baseWeight = 4.0
		case TagKindCharacter:
			baseWeight = 3.2
		case TagKindCopyright:
			baseWeight = 2.2
		case TagKindGeneral:
			pop := 0
			if freqMap != nil {
				pop = freqMap[t]
			}
			if pop > 0 {
				baseWeight = 1.0 / (1.0 + 0.35*math.Log10(float64(pop)+1))
			} else {
				baseWeight = 1.4
			}
		}

		res = append(res, CategorizedTag{
			Tag:    t,
			Kind:   kind,
			Weight: baseWeight,
		})
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Weight > res[j].Weight
	})

	return res
}

// BuildRelatedQueries формирует первичный и fallback поисковые запросы
func BuildRelatedQueries(analyzed []CategorizedTag, mode string) (primaryQuery string, fallbackQuery string, pickedTags []string) {
	var artists []string
	var characters []string
	var copyrights []string
	var generals []string

	for _, ct := range analyzed {
		switch ct.Kind {
		case TagKindArtist:
			artists = append(artists, ct.Tag)
		case TagKindCharacter:
			characters = append(characters, ct.Tag)
		case TagKindCopyright:
			copyrights = append(copyrights, ct.Tag)
		case TagKindGeneral:
			generals = append(generals, ct.Tag)
		}
	}

	switch mode {
	case "artist":
		if len(artists) > 0 {
			primaryQuery = artists[0]
			pickedTags = []string{artists[0]}
			if len(characters) > 0 {
				primaryQuery = artists[0] + " " + characters[0]
				fallbackQuery = artists[0]
				pickedTags = append(pickedTags, characters[0])
			}
			return
		}
	case "character", "char":
		if len(characters) > 0 {
			primaryQuery = characters[0]
			pickedTags = []string{characters[0]}
			if len(generals) > 0 {
				primaryQuery = characters[0] + " " + generals[0]
				fallbackQuery = characters[0]
				pickedTags = append(pickedTags, generals[0])
			}
			return
		} else if len(copyrights) > 0 {
			primaryQuery = copyrights[0]
			pickedTags = []string{copyrights[0]}
			if len(generals) > 0 {
				primaryQuery = copyrights[0] + " " + generals[0]
				fallbackQuery = copyrights[0]
				pickedTags = append(pickedTags, generals[0])
			}
			return
		}
	}

	// Режим "all" — сбалансированный поиск
	if len(artists) > 0 && len(characters) > 0 {
		primaryQuery = artists[0] + " " + characters[0]
		fallbackQuery = characters[0]
		pickedTags = []string{artists[0], characters[0]}
		return
	}
	if len(characters) > 0 {
		if len(generals) > 0 {
			primaryQuery = characters[0] + " " + generals[0]
			fallbackQuery = characters[0]
			pickedTags = []string{characters[0], generals[0]}
		} else {
			primaryQuery = characters[0]
			pickedTags = []string{characters[0]}
		}
		return
	}
	if len(artists) > 0 {
		if len(generals) > 0 {
			primaryQuery = artists[0] + " " + generals[0]
			fallbackQuery = artists[0]
			pickedTags = []string{artists[0], generals[0]}
		} else {
			primaryQuery = artists[0]
			pickedTags = []string{artists[0]}
		}
		return
	}
	if len(copyrights) > 0 && len(generals) > 0 {
		primaryQuery = copyrights[0] + " " + generals[0]
		fallbackQuery = copyrights[0]
		pickedTags = []string{copyrights[0], generals[0]}
		return
	}

	var topGen []string
	for i := 0; i < len(generals) && i < 2; i++ {
		topGen = append(topGen, generals[i])
	}
	if len(topGen) > 0 {
		primaryQuery = strings.Join(topGen, " ")
		pickedTags = topGen
		if len(topGen) > 1 {
			fallbackQuery = topGen[0]
		}
	}
	return
}

// RelatedCandidate унифицированный кандидат для ранжирования похожих
type RelatedCandidate struct {
	ID         int
	Tags       string
	FileURL    string
	SampleURL  string
	PreviewURL string
	FileType   string
	Width      int
	Height     int
	Score      int
	Rating     string
	Phash      string
	Downloaded bool
}

// ScoredRelatedCandidate кандидат с вычисленным скором и типом совпадения
type ScoredRelatedCandidate struct {
	Candidate   RelatedCandidate
	Score       float64
	Similarity  int    // 0..100
	MatchType   string // "artist", "character", "visual", "tags"
	MatchedTags []string
}

// RerankRelated вычисляет взвешенную схожесть кандидатов с исходным постом
func RerankRelated(
	srcTags string,
	srcPHash string,
	candidates []RelatedCandidate,
	freqMap map[string]int,
	hiddenTags map[string]bool,
	favTags map[string]bool,
) []ScoredRelatedCandidate {
	srcAnalyzed := AnalyzeTags(srcTags, freqMap)
	srcTagWeights := make(map[string]float64, len(srcAnalyzed))
	srcArtists := make(map[string]bool)
	srcCharacters := make(map[string]bool)

	for _, ct := range srcAnalyzed {
		srcTagWeights[ct.Tag] = ct.Weight
		if ct.Kind == TagKindArtist {
			srcArtists[ct.Tag] = true
		} else if ct.Kind == TagKindCharacter {
			srcCharacters[ct.Tag] = true
		}
	}

	scored := make([]ScoredRelatedCandidate, 0, len(candidates))

	for _, cand := range candidates {
		candTags := strings.Fields(cand.Tags)
		candTagSet := make(map[string]bool, len(candTags))
		hasHidden := false

		for _, t := range candTags {
			norm := strings.ToLower(strings.TrimLeft(t, "+-"))
			if hiddenTags[norm] {
				hasHidden = true
				break
			}
			candTagSet[norm] = true
		}
		if hasHidden {
			continue
		}

		var intersectWeight, unionWeight float64
		var matchedTags []string
		hasSharedArtist := false
		hasSharedChar := false

		allTags := make(map[string]bool)
		for t := range srcTagWeights {
			allTags[t] = true
		}
		for t := range candTagSet {
			allTags[t] = true
		}

		for t := range allTags {
			w := 1.0
			if sw, ok := srcTagWeights[t]; ok {
				w = sw
			}
			inSrc := srcTagWeights[t] > 0
			inCand := candTagSet[t]

			if inSrc && inCand {
				intersectWeight += w
				unionWeight += w
				matchedTags = append(matchedTags, t)
				if srcArtists[t] {
					hasSharedArtist = true
				}
				if srcCharacters[t] {
					hasSharedChar = true
				}
			} else {
				unionWeight += w
			}
		}

		jaccard := 0.0
		if unionWeight > 0 {
			jaccard = intersectWeight / unionWeight
		}

		// Визуальный бонус pHash при наличии
		visualBonus := 0.0
		isVisualMatch := false
		if srcPHash != "" && cand.Phash != "" {
			if d, ok := HammingPHash(srcPHash, cand.Phash); ok && d <= 16 {
				visualBonus = float64(16-d) / 16.0 * 0.45
				if d <= 10 {
					isVisualMatch = true
				}
			}
		}

		favBonus := 0.0
		for t := range candTagSet {
			if favTags[t] {
				favBonus = 0.15
				break
			}
		}

		totalScore := jaccard + visualBonus + favBonus

		matchType := "tags"
		if isVisualMatch {
			matchType = "visual"
		} else if hasSharedArtist {
			matchType = "artist"
		} else if hasSharedChar {
			matchType = "character"
		}

		simPct := int(math.Min(99, math.Max(15, (jaccard*1.4+visualBonus)*100)))

		sort.Slice(matchedTags, func(i, j int) bool {
			return srcTagWeights[matchedTags[i]] > srcTagWeights[matchedTags[j]]
		})
		if len(matchedTags) > 3 {
			matchedTags = matchedTags[:3]
		}

		scored = append(scored, ScoredRelatedCandidate{
			Candidate:   cand,
			Score:       totalScore,
			Similarity:  simPct,
			MatchType:   matchType,
			MatchedTags: matchedTags,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Candidate.Score > scored[j].Candidate.Score
	})

	return scored
}
