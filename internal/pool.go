package internal

import "sync"

// pool64k кэширует 64КБ буферы для стриминга прокси и скачивания.
var pool64k = sync.Pool{
	New: func() any {
		b := make([]byte, 64<<10)
		return &b
	},
}

// pool128k кэширует 128КБ буферы для сегментных медиа-запросов и ZIP-архивов.
var pool128k = sync.Pool{
	New: func() any {
		b := make([]byte, 128<<10)
		return &b
	},
}

// GetBuffer64k получает слайс []byte длиной 64KB из пула.
func GetBuffer64k() *[]byte {
	return pool64k.Get().(*[]byte)
}

// PutBuffer64k возвращает слайс []byte в пул.
func PutBuffer64k(b *[]byte) {
	if b != nil && len(*b) == 64<<10 {
		pool64k.Put(b)
	}
}

// GetBuffer128k получает слайс []byte длиной 128KB из пула.
func GetBuffer128k() *[]byte {
	return pool128k.Get().(*[]byte)
}

// PutBuffer128k возвращает слайс []byte в пул.
func PutBuffer128k(b *[]byte) {
	if b != nil && len(*b) == 128<<10 {
		pool128k.Put(b)
	}
}
