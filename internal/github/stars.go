package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/apex/log"
	"golang.org/x/sync/errgroup"
)

var (
	errNoMorePages  = errors.New("no more pages to get")
	ErrTooManyStars = errors.New("repo has too many stargazers, github won't allow us to list all stars")
)

// Stargazer is a star at a given time.
// 记录的每个star的时间
type Stargazer struct {
	StarredAt time.Time `json:"starred_at"`
}

// Stargazers returns all the stargazers of a given repo.
func (gh *GitHub) Stargazers(ctx context.Context, repo Repository) (stars []Stargazer, err error) {
	sem := make(chan bool, 4)

	if gh.totalPages(repo) > 400 {
		// 做了限制，star的总页数超过400就不展示了？
		// 是不是可以继续做？
		return stars, ErrTooManyStars
	}

	var g errgroup.Group
	var lock sync.Mutex
	for page := 1; page <= gh.lastPage(repo); page++ {
		sem <- true
		page := page
		g.Go(func() error {
			defer func() { <-sem }()
			result, err := gh.getStargazersPage(ctx, repo, page)
			if errors.Is(err, errNoMorePages) {
				return nil
			}
			if err != nil {
				return err
			}
			lock.Lock()
			defer lock.Unlock()
			//将切片 result 中的元素追加到切片 stars 的末尾。
			//在Go语言中，append() 函数用于向切片中追加元素。
			//它接受一个切片作为第一个参数，并将要追加的元素作为后续参数传入。在这个特殊的语法中，...
			//表示将切片 result 拆分为单独的元素，然后将这些元素追加到 stars 切片中。
			stars = append(stars, result...)
			return nil
		})
	}
	err = g.Wait()
	sort.Slice(stars, func(i, j int) bool {
		return stars[i].StarredAt.Before(stars[j].StarredAt)
	})
	return
}

// 缓存设计
// - get last modified from cache
//   - if exists, hit api with it
//     - if it returns 304, get from cache
//       - if succeeds, return it
//       - if fails, it means we dont have that page in cache, hit api again
//         - if succeeds, cache and return both the api and header
//         - if fails, return error
//   - if not exists, hit api
//     - if succeeds, cache and return both the api and header
//     - if fails, return error

// nolint: funlen
// TODO: refactor.
func (gh *GitHub) getStargazersPage(ctx context.Context, repo Repository, page int) ([]Stargazer, error) {
	log := log.WithField("repo", repo.FullName).WithField("page", page)
	defer log.Trace("get page").Stop(nil)

	var stars []Stargazer
	key := fmt.Sprintf("%s_%d", repo.FullName, page)
	etagKey := fmt.Sprintf("%s_%d", repo.FullName, page) + "_etag"

	// 读缓存，没命中就发请求
	var etag string
	if err := gh.cache.Get(etagKey, &etag); err != nil {
		log.WithError(err).Warnf("failed to get %s from cache", etagKey)
	}

	resp, err := gh.makeStarPageRequest(ctx, repo, page, etag)
	if err != nil {
		return stars, err
	}

	bts, err := io.ReadAll(resp.Body)
	if err != nil {
		return stars, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	// 304（未修改）自从上次请求后，请求的网页未修改过，直接拿缓存，这样就不会拿到过期数据
	case http.StatusNotModified:
		effectiveEtags.Inc()
		log.Info("not modified")
		err := gh.cache.Get(key, &stars)
		if err != nil {
			log.WithError(err).Warnf("failed to get %s from cache", key)
			if err := gh.cache.Delete(etagKey); err != nil {
				log.WithError(err).Warnf("failed to delete %s from cache", etagKey)
			}
			// 从缓存里拿
			return gh.getStargazersPage(ctx, repo, page)
		}
		return stars, err
	case http.StatusForbidden:
		rateLimits.Inc()
		log.Warn("rate limit hit")
		return stars, ErrRateLimit
	case http.StatusOK:
		// 使用json.Unmarshal函数对一个字节切片进行反序列化，并将结果存储到stars变量中
		if err := json.Unmarshal(bts, &stars); err != nil {
			return stars, err
		}
		if len(stars) == 0 {
			return stars, errNoMorePages
		}
		// 放在缓存里
		if err := gh.cache.Put(key, stars); err != nil {
			log.WithError(err).Warnf("failed to cache %s", key)
		}

		// ETag（Entity Tag）是用于标识资源内容的标记。它是由服务器生成并返回给客户端的一个字符串值。
		//每当资源的内容发生变化时，ETag的值也会相应地改变。
		// ETag可以用作缓存机制的一部分，用于验证资源的有效性和完整性。客户端在发送请求时可以在请求头中包含If-None-Match字段，
		//并将上次获取的ETag值作为其值。服务器在收到这个请求后，会检查资源的ETag值是否与客户端提供的值匹配。如果匹配，服务器会返回
		//一个特殊的304 Not Modified响应，表示资源未发生变化，客户端可以使用缓存的版本。如果ETag值不匹配，服务器会返回资源的最新版本，
		//并更新ETag的值。
		etag = resp.Header.Get("etag")
		if etag != "" {
			if err := gh.cache.Put(etagKey, etag); err != nil {
				log.WithError(err).Warnf("failed to cache %s", etagKey)
			}
		}

		return stars, nil
	default:
		return stars, fmt.Errorf("%w: %v", ErrGitHubAPI, string(bts))
	}
}

func (gh *GitHub) totalPages(repo Repository) int {
	return repo.StargazersCount / gh.pageSize
}

func (gh *GitHub) lastPage(repo Repository) int {
	return gh.totalPages(repo) + 1
}

func (gh *GitHub) makeStarPageRequest(ctx context.Context, repo Repository, page int, etag string) (*http.Response, error) {
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/stargazers?page=%d&per_page=%d",
		repo.FullName,
		page,
		gh.pageSize,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/vnd.github.v3.star+json")
	if etag != "" {
		req.Header.Add("If-None-Match", etag)
	}

	return gh.authorizedDo(req, 0)
}
