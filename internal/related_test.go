package internal

import (
	"testing"
)

func TestClassifyTag(t *testing.T) {
	tests := []struct {
		tag  string
		want TagKind
	}{
		{"artist:ciloranko", TagKindArtist},
		{"ciloranko_(artist)", TagKindArtist},
		{"morikura_en_(style)", TagKindArtist},
		{"character:rem", TagKindCharacter},
		{"rem_(re:zero)", TagKindCharacter},
		{"asuka_langley_(evangelion)", TagKindCharacter},
		{"copyright:re:zero", TagKindCopyright},
		{"series:fate", TagKindCopyright},
		{"fate/stay_night_(series)", TagKindCopyright},
		{"highres", TagKindMeta},
		{"1girl", TagKindMeta},
		{"solo", TagKindMeta},
		{"cyberpunk_jacket", TagKindGeneral},
		{"cat_ears", TagKindGeneral},
	}

	for _, tc := range tests {
		got := ClassifyTag(tc.tag)
		if got != tc.want {
			t.Errorf("ClassifyTag(%q) = %v; want %v", tc.tag, got, tc.want)
		}
	}
}

func TestAnalyzeTags(t *testing.T) {
	freq := map[string]int{
		"cat_ears":  5000,
		"rare_mech": 2,
	}

	tags := "1girl artist:reina rem_(re:zero) cat_ears rare_mech highres"
	analyzed := AnalyzeTags(tags, freq)

	if len(analyzed) < 4 {
		t.Fatalf("expected at least 4 analyzed tags, got %d", len(analyzed))
	}

	// First should be artist or character due to high weights (4.0 and 3.2)
	if analyzed[0].Kind != TagKindArtist && analyzed[0].Kind != TagKindCharacter {
		t.Errorf("top weighted tag should be artist or character, got %v (%s)", analyzed[0].Kind, analyzed[0].Tag)
	}

	// Meta tags like 1girl and highres must be excluded
	for _, ct := range analyzed {
		if ct.Tag == "1girl" || ct.Tag == "highres" {
			t.Errorf("meta/generic tag %s should be excluded", ct.Tag)
		}
	}

	// rare_mech should have higher weight than common cat_ears
	var rareW, commonW float64
	for _, ct := range analyzed {
		if ct.Tag == "rare_mech" {
			rareW = ct.Weight
		} else if ct.Tag == "cat_ears" {
			commonW = ct.Weight
		}
	}
	if rareW <= commonW {
		t.Errorf("rare tag weight (%f) should be higher than common tag weight (%f)", rareW, commonW)
	}
}

func TestBuildRelatedQueries(t *testing.T) {
	analyzed := []CategorizedTag{
		{Tag: "artist:reina", Kind: TagKindArtist, Weight: 4.0},
		{Tag: "rem_(re:zero)", Kind: TagKindCharacter, Weight: 3.2},
		{Tag: "maid_outfit", Kind: TagKindGeneral, Weight: 1.5},
	}

	// Mode "all"
	q, fb, picked := BuildRelatedQueries(analyzed, "all")
	if q != "artist:reina rem_(re:zero)" {
		t.Errorf("all query = %q; want 'artist:reina rem_(re:zero)'", q)
	}
	if fb != "rem_(re:zero)" {
		t.Errorf("all fallback = %q; want 'rem_(re:zero)'", fb)
	}
	if len(picked) != 2 {
		t.Errorf("picked = %v; want 2 tags", picked)
	}

	// Mode "character"
	qChar, _, _ := BuildRelatedQueries(analyzed, "character")
	if qChar != "rem_(re:zero) maid_outfit" {
		t.Errorf("character query = %q; want 'rem_(re:zero) maid_outfit'", qChar)
	}

	// Mode "artist"
	qArt, _, _ := BuildRelatedQueries(analyzed, "artist")
	if qArt != "artist:reina rem_(re:zero)" {
		t.Errorf("artist query = %q", qArt)
	}
}

func TestRerankRelated(t *testing.T) {
	srcTags := "artist:ke-ta rem_(re:zero) maid_outfit blue_hair short_hair"
	srcPHash := "ffff0000ffff0000"

	candidates := []RelatedCandidate{
		{
			ID:    1,
			Tags:  "1girl smile blue_hair",
			Score: 10,
		},
		{
			ID:    2,
			Tags:  "artist:ke-ta rem_(re:zero) maid_outfit",
			Score: 50,
			Phash: "ffff0000ffff0001", // Hamming distance 1 (near visual duplicate)
		},
		{
			ID:    3,
			Tags:  "rem_(re:zero) school_uniform",
			Score: 20,
		},
		{
			ID:    4,
			Tags:  "rem_(re:zero) gore_blood", // has hidden tag
			Score: 99,
		},
	}

	hidden := map[string]bool{"gore_blood": true}
	fav := map[string]bool{"maid_outfit": true}

	ranked := RerankRelated(srcTags, srcPHash, candidates, nil, hidden, fav)

	// Post 4 should be filtered out because of hidden tag
	for _, r := range ranked {
		if r.Candidate.ID == 4 {
			t.Errorf("post 4 with hidden tag must be filtered out")
		}
	}

	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked posts, got %d", len(ranked))
	}

	// Post 2 should be ranked 1st: same artist, same character, maid outfit + visual pHash bonus
	if ranked[0].Candidate.ID != 2 {
		t.Errorf("post 2 should be ranked 1st, got post %d", ranked[0].Candidate.ID)
	}
	if ranked[0].MatchType != "visual" && ranked[0].MatchType != "artist" {
		t.Errorf("post 2 match type = %s", ranked[0].MatchType)
	}
	if ranked[0].Similarity < 70 {
		t.Errorf("post 2 similarity should be high, got %d", ranked[0].Similarity)
	}
}

func TestSimilarPHashAllBits(t *testing.T) {
	db := NewPostDB(t.TempDir() + "/phash_test.db")
	defer db.Close()

	// target: 0x8000_0000_0000_0000 (top bit is 1)
	targetHash := "8000000000000000"
	// other1: 0x0000_0000_0000_0000 (differs only in bit 63! Hamming dist = 1)
	// Under the old phash_seed (seed-1, seed, seed+1), this was MISSED because seed difference was 32768.
	closeHash := "0000000000000000"
	farHash := "00000000ffffffff" // Hamming dist = 33

	db.AddOrUpdate(&Post{ID: 10, Downloaded: true, Phash: closeHash, Tags: "test"})
	db.AddOrUpdate(&Post{ID: 20, Downloaded: true, Phash: farHash, Tags: "test"})

	similar := db.SimilarPHash(targetHash, -1, 10, 12)
	if len(similar) != 1 || similar[0].ID != 10 {
		t.Fatalf("expected post 10 to be found despite top bit difference, got %v", similar)
	}

	bestDup := db.FindDownloadedByPHash(targetHash, -1, 12)
	if bestDup != 10 {
		t.Errorf("FindDownloadedByPHash should find post 10, got %d", bestDup)
	}
}
