package internal

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"

	"github.com/disintegration/imaging"
)

// patternImage строит фото-подобную картинку: градиент + лёгкая текстура +
// круг с чёткими краями (стабильный pHash-«скелет»). shift сдвигает фазу
// фона — «похожая, но другая» картинка.
func patternImage(size, shift int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px := (x + shift*4) % size
			r := uint8(40 + (px*140)/size + (x*7+y*5)%40)
			g := uint8(60 + (y*110)/size + (x*11+y*3)%35)
			b := uint8((px*100)/size + (x*13+y*7)%30)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	// Диск с резким краем — структура, которую JPEG не «съедает» целиком.
	rad := size / 6
	ccx, ccy := size*3/4, size/3
	for y := ccy - rad; y <= ccy+rad; y++ {
		for x := ccx - rad; x <= ccx+rad; x++ {
			if (x-ccx)*(x-ccx)+(y-ccy)*(y-ccy) <= rad*rad {
				img.Set(x, y, color.RGBA{R: 220, G: 60, B: 90, A: 255})
			}
		}
	}
	return img
}

// resizeCopy — «пережанная копия» уменьшением/увеличением: пиксели меняются,
// содержимое сохраняется. Детерминированный аналог ре-энкода.
func resizeCopy(t *testing.T, src image.Image) image.Image {
	t.Helper()
	small := imaging.Resize(src, 96, 0, imaging.Lanczos)
	return imaging.Resize(small, 128, 0, imaging.Lanczos)
}

func TestFindDownloadedByPHash(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "dups.db"))
	t.Cleanup(func() { db.Close() })

	imgA := patternImage(128, 0)
	imgB := resizeCopy(t, imgA) // пережатая копия

	hashA, hashB := PerceptualHash(imgA), PerceptualHash(imgB)
	if hashA == "" || hashB == "" {
		t.Fatal("pHash пустой — картинки не распознаны")
	}
	dist, _ := HammingPHash(hashA, hashB)
	if dist > 5 {
		t.Fatalf("пережатая копия слишком далеко от оригинала: dist=%d (порог дедупа 5)", dist)
	}

	db.AddOrUpdate(&Post{ID: 10, Tags: "test", FilePath: filepath.Join("data", "posts", "10", "original.jpg"), Downloaded: true, Phash: hashA, Source: "rule34"})

	// Пережатая копия находит оригинал.
	if got := db.FindDownloadedByPHash(hashB, 11, 5); got != 10 {
		t.Errorf("FindDownloadedByPHash(copy) = %d, ожидался 10", got)
	}
	// Свой же пост исключается из кандидатов.
	if got := db.FindDownloadedByPHash(hashB, 10, 64); got != 0 {
		t.Errorf("excludeID не работает: %d", got)
	}
	// Совсем другой образ (другая фаза) не совпадает при точном сравнении.
	far := PerceptualHash(patternImage(128, 40))
	if got := db.FindDownloadedByPHash(far, 11, 0); got != 0 {
		t.Errorf("разный образ не должен быть дубликатом при maxDist=0: %d", got)
	}
	// Пустой/битый pHash не даёт ложных совпадений.
	if got := db.FindDownloadedByPHash("", 11, 64); got != 0 {
		t.Errorf("пустой pHash: %d", got)
	}

	// Source: проставляется и не затирается повторным апсертом.
	if p := db.Get(10); p.Source != "rule34" {
		t.Errorf("source после апсерта = %q", p.Source)
	}
	db.SetPostSource(10, "gelbooru")
	db.AddOrUpdate(&Post{ID: 10, Tags: "test", Downloaded: true, Source: "safebooru"})
	if p := db.Get(10); p.Source != "gelbooru" {
		t.Errorf("source перезаписан при upsert: %q", p.Source)
	}
}

func TestProfileReplacePostID(t *testing.T) {
	p := NewProfile(filepath.Join(t.TempDir(), "profile.json"))
	p.LikedPosts[1] = true
	p.LikedAt[1] = 100
	p.LikedPosts[2] = true
	p.LikedAt[2] = 250
	p.HiddenPosts[3] = true
	p.Collections = []Collection{
		{ID: "c1", Name: "коллекция", Posts: []int{3, 2, 5, 1}},
	}

	p.ReplacePostID(2, 1)

	if !p.LikedPosts[1] || p.LikedPosts[2] {
		t.Errorf("лайки: после merge должны остаться только у поста 1: %v", p.LikedPosts)
	}
	if p.LikedAt[2] != 0 || p.LikedAt[1] != 250 {
		t.Errorf("LikedAt: должна сохраниться максимальная дата (250): %v", p.LikedAt)
	}
	if !p.HiddenPosts[3] {
		t.Error("скрытия других постов не должны теряться")
	}
	got := p.Collections[0].Posts
	want := []int{3, 1, 5} // 2→1, существующая 1 схлопнута
	if len(got) != len(want) {
		t.Fatalf("коллекция: %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("коллекция: %v, ожидалось %v", got, want)
		}
	}
}

func TestReplaceCommentsPostAndDeleteRow(t *testing.T) {
	db := NewPostDB(filepath.Join(t.TempDir(), "merge.db"))
	t.Cleanup(func() { db.Close() })

	db.AddOrUpdate(&Post{ID: 1, Tags: "keep", Downloaded: true, FilePath: "data/posts/1/original.jpg"})
	db.AddOrUpdate(&Post{ID: 2, Tags: "dup", Downloaded: true, FilePath: "data/posts/2/original.jpg"})
	if _, err := db.AddComment(2, "alice", "дубликат поста"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	db.ReplaceCommentsPost(2, 1)

	coms := db.Comments(1)
	if len(coms) != 1 || coms[0].PostID != 1 {
		t.Fatalf("комментарий не перенесён: %+v", coms)
	}
	if n := len(db.Comments(2)); n != 0 {
		t.Fatalf("у дубликата осталось %d комментариев", n)
	}

	db.DeletePostRow(2)
	if p := db.Get(2); p != nil {
		t.Error("запись поста 2 не удалена")
	}
	// Теги удалённого поста уходят каскадом, а keep не тронут.
	if p := db.Get(1); p == nil || p.Tags != "keep" {
		t.Error("keep-пост повреждён")
	}
}

func TestDupPHashThreshold(t *testing.T) {
	if got := dupPHashThreshold(); got != 5 {
		t.Errorf("порог по умолчанию = %d, ожидался 5", got)
	}
	t.Setenv("BRIEFLY_DUP_PHASH_THRESHOLD", "3")
	if got := dupPHashThreshold(); got != 3 {
		t.Errorf("порог 3: %d", got)
	}
	t.Setenv("BRIEFLY_DUP_PHASH_THRESHOLD", "bogus")
	if got := dupPHashThreshold(); got != 5 {
		t.Errorf("мусор в env должен дать дефолт 5: %d", got)
	}
	t.Setenv("BRIEFLY_DUP_PHASH_THRESHOLD", "99")
	if got := dupPHashThreshold(); got != 5 {
		t.Errorf("порог вне диапазона должен дать 5: %d", got)
	}
}
