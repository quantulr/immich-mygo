package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"

	"immich-mygo/immichapi"
)

// feedLimit is how many assets the timeline loads at first: enough to fill
// the window, and a few screens more.
const feedLimit = 60

// photo is an asset of the timeline, and its thumbnail once it loads.
type photo struct {
	ID    string
	Video bool
	Bm    *ui.Bitmap
}

// album is an album of the server, and the thumbnail of its cover once it
// loads.
type album struct {
	ID    string
	Name  string
	Count int
	// Cover is the asset whose thumbnail stands for the album, or "".
	Cover string
	Bm    *ui.Bitmap
}

// fetchTimeline returns up to limit assets of the user's timeline, newest
// first, or of their favorites.
func fetchTimeline(base, token string, favorites bool, limit int) ([]*photo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	order, visibility := immichapi.AssetOrderDesc, immichapi.Timeline
	params := &immichapi.GetTimeBucketsParams{Order: &order, Visibility: &visibility}
	if favorites {
		yes := true
		params.IsFavorite = &yes
	}
	resp, err := client.GetTimeBucketsWithResponse(ctx, params)
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errors.New("服务器没有返回时间线")
	}

	var photos []*photo
	for _, bucket := range *resp.JSON200 {
		if len(photos) >= limit {
			break
		}
		assets, err := fetchBucket(ctx, client, bucket.TimeBucket, order, visibility, favorites)
		if err != nil {
			return nil, err
		}
		photos = append(photos, assets...)
	}
	if len(photos) > limit {
		photos = photos[:limit]
	}
	return photos, nil
}

// fetchBucket returns the assets of one bucket of the timeline.
func fetchBucket(ctx context.Context, client *immichapi.ClientWithResponses, bucket string, order immichapi.AssetOrder, visibility immichapi.AssetVisibility, favorites bool) ([]*photo, error) {
	params := &immichapi.GetTimeBucketParams{TimeBucket: bucket, Order: &order, Visibility: &visibility}
	if favorites {
		yes := true
		params.IsFavorite = &yes
	}
	resp, err := client.GetTimeBucketWithResponse(ctx, params)
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errors.New("服务器没有返回照片")
	}

	d := resp.JSON200
	photos := make([]*photo, 0, len(d.Id))
	for i, id := range d.Id {
		p := &photo{ID: id}
		if i < len(d.IsImage) {
			p.Video = !d.IsImage[i]
		}
		photos = append(photos, p)
	}
	return photos, nil
}

// fetchAlbums returns the user's albums.
func fetchAlbums(base, token string) ([]*album, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetAllAlbumsWithResponse(ctx, &immichapi.GetAllAlbumsParams{})
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errors.New("服务器没有返回相册")
	}

	albums := make([]*album, 0, len(*resp.JSON200))
	for _, d := range *resp.JSON200 {
		a := &album{ID: d.Id.String(), Name: d.AlbumName, Count: d.AssetCount}
		if d.AlbumThumbnailAssetId != nil {
			a.Cover = d.AlbumThumbnailAssetId.String()
		}
		albums = append(albums, a)
	}
	return albums, nil
}

// fetchThumbnail returns the thumbnail of an asset.
func fetchThumbnail(base, token, id string) (*ui.Bitmap, error) {
	assetID, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	size := immichapi.AssetMediaSizeThumbnail
	resp, err := client.ViewAssetWithResponse(ctx, assetID, &immichapi.ViewAssetParams{Size: &size})
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	bm, err := ui.DecodeBitmap(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("无法读取缩略图：%v", err)
	}
	return bm, nil
}

// statusErr names a response status in the user's words; a refused token
// is errSessionExpired.
func statusErr(code int, status string) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return errSessionExpired
	case code == http.StatusNotFound:
		return errors.New("没有找到 Immich 接口，请确认服务器地址")
	default:
		return fmt.Errorf("服务器返回了 %s", status)
	}
}
