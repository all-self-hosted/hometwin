package ikea

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	maxPage   = 8 << 20
)

// NewClient is an HTTP client for product pages. It goes through HTTPS_PROXY / HTTP_PROXY
// (minus NO_PROXY) when they are set, since shops abroad may need a proxy.
func NewClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyFromEnvironment
	return &http.Client{Transport: tr, Timeout: 25 * time.Second}
}

// Fetch downloads a product page and parses it. client nil means NewClient().
func Fetch(ctx context.Context, client *http.Client, rawURL string) (Product, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Product{}, errors.New("链接不对：要以 http:// 或 https:// 开头的商品页地址")
	}
	if client == nil {
		client = NewClient()
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Product{}, errors.New("链接不对：" + err.Error())
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Product{}, errors.New("商品页打开超时，网络可能需要代理（设置 HTTPS_PROXY）")
		}
		return Product{}, fmt.Errorf("商品页打不开：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Product{}, fmt.Errorf("商品页打不开（HTTP %d）", resp.StatusCode)
	}
	final := resp.Request.URL
	if strings.Contains(final.Path, "/cat/") {
		return Product{}, errors.New("这个链接不是单个商品页（可能已下架，跳到了分类页）")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPage+1))
	if err != nil {
		return Product{}, fmt.Errorf("商品页没有读完：%v", err)
	}
	if len(body) > maxPage {
		return Product{}, errors.New("页面太大，不像商品页")
	}
	p, err := Parse(body, final.String())
	if err != nil && p.Name == "" {
		return p, errors.New("这个链接不是单个商品页，页面上没有找到商品")
	}
	return p, err
}
