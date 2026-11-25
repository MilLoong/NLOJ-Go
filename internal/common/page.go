package common

type Page[T any] struct {
	Num     int `json:"pageNum"`
	Size    int `json:"pageSize"`
	Total   int `json:"total"`
	Records []T `json:"records"`
}

func Paginate[T any](items []T, n, size, cursor int, match func(T) (int, bool)) Page[T] {
	p := Page[T]{Num: n, Size: size, Records: make([]T, 0, size)}
	for i := len(items) - 1; i >= 0; i-- {
		id, ok := match(items[i])
		if !ok {
			continue
		}
		rank := p.Total
		p.Total++
		if len(p.Records) == size || (cursor > 0 && id >= cursor) || (cursor == 0 && rank/size < n-1) {
			continue
		}
		p.Records = append(p.Records, items[i])
	}
	return p
}
