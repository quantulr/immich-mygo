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

// timelineDay is a day of the timeline: how many assets the server says it
// holds, and the assets themselves once the day loads.
type timelineDay struct {
	key   string
	count int
	// photos is nil until the day loads; loaded tells that it did, as an
	// empty day has none.
	photos  []*photo
	loaded  bool
	loading bool
	err     error
}

// photo is an asset of the timeline, and its thumbnail once it loads.
type photo struct {
	ID    string
	Video bool
	// Duration is how long a video runs, in milliseconds.
	Duration int
	Bm       *ui.Bitmap
}

// assetDetails is what the image page's details panel tells of an asset.
type assetDetails struct {
	Name        string
	Taken       time.Time
	TimeZone    string
	Width       int
	Height      int
	Size        int64
	Make        string
	Model       string
	Lens        string
	FNumber     float64
	Exposure    string
	ISO         int
	Focal       float64
	City        string
	State       string
	Country     string
	Latitude    *float64
	Longitude   *float64
	Description string
	// Duration is how long a video runs, in milliseconds.
	Duration int
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

// timelineSource is whose assets a timeline shows: the user's, their
// favorites, or an album's.
type timelineSource struct {
	favorites bool
	// album is the ID of the album whose assets the timeline shows, or "".
	album string
}

// fetchTimelineDays returns the days of a timeline, newest first, with how
// many assets each holds. The assets of a day load as the timeline shows
// it.
func fetchTimelineDays(base, token string, src timelineSource) ([]*timelineDay, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	order := immichapi.AssetOrderDesc
	albumID, favorite, visibility, err := sourceParams(src)
	if err != nil {
		return nil, err
	}
	params := &immichapi.GetTimeBucketsParams{
		Order:      &order,
		AlbumId:    albumID,
		IsFavorite: favorite,
		Visibility: visibility,
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

	days := make([]*timelineDay, 0, len(*resp.JSON200))
	for _, bucket := range *resp.JSON200 {
		if bucket.Count <= 0 {
			continue
		}
		days = append(days, &timelineDay{key: bucket.TimeBucket, count: bucket.Count})
	}
	return days, nil
}

// sourceParams is what a timeline's queries ask for: an album's assets, or
// those of the whole timeline, or its favorites. An album shows its assets
// whatever their visibility.
func sourceParams(src timelineSource) (albumID *uuid.UUID, favorite *bool, visibility *immichapi.AssetVisibility, err error) {
	if src.album != "" {
		id, err := uuid.Parse(src.album)
		if err != nil {
			return nil, nil, nil, err
		}
		albumID = &id
	} else {
		visible := immichapi.Timeline
		visibility = &visible
	}
	if src.favorites {
		yes := true
		favorite = &yes
	}
	return albumID, favorite, visibility, nil
}

// fetchDayAssets returns the assets of one day of a timeline, newest
// first.
func fetchDayAssets(base, token, bucket string, src timelineSource) ([]*photo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	return fetchBucket(ctx, client, bucket, src)
}

// fetchBucket returns the assets of one bucket of a timeline.
func fetchBucket(ctx context.Context, client *immichapi.ClientWithResponses, bucket string, src timelineSource) ([]*photo, error) {
	order := immichapi.AssetOrderDesc
	albumID, favorite, visibility, err := sourceParams(src)
	if err != nil {
		return nil, err
	}
	params := &immichapi.GetTimeBucketParams{
		TimeBucket: bucket,
		Order:      &order,
		AlbumId:    albumID,
		IsFavorite: favorite,
		Visibility: visibility,
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
		if i < len(d.Duration) && d.Duration[i] != nil {
			p.Duration = *d.Duration[i]
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
	return fetchAssetImage(base, token, id, immichapi.AssetMediaSizeThumbnail, "缩略图")
}

// fetchPreview returns the larger image of an asset, as the image page
// shows it: the size Immich keeps for the screen, short of the original's.
func fetchPreview(base, token, id string) (*ui.Bitmap, error) {
	return fetchAssetImage(base, token, id, immichapi.AssetMediaSizePreview, "图片")
}

// fetchAssetImage returns one of the images Immich stores of an asset.
func fetchAssetImage(base, token, id string, size immichapi.AssetMediaSize, what string) (*ui.Bitmap, error) {
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
	resp, err := client.ViewAssetWithResponse(ctx, assetID, &immichapi.ViewAssetParams{Size: &size})
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	bm, err := ui.DecodeBitmap(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("无法读取%s：%v", what, err)
	}
	return bm, nil
}

// fetchAssetDetails returns what the image page's details panel tells of an
// asset: its name and time, its dimensions and size, and the camera, lens
// and place its EXIF data names.
func fetchAssetDetails(base, token, id string) (*assetDetails, error) {
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
	resp, err := client.GetAssetInfoWithResponse(ctx, assetID, &immichapi.GetAssetInfoParams{})
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status()); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errors.New("服务器没有返回详情")
	}

	d := resp.JSON200
	ad := &assetDetails{
		Name:     d.OriginalFileName,
		Taken:    d.LocalDateTime,
		Width:    intOf(d.Width),
		Height:   intOf(d.Height),
		Duration: intOf(d.Duration),
	}
	if e := d.ExifInfo; e != nil {
		ad.TimeZone = stringOf(e.TimeZone)
		ad.Size = int64(intOf(e.FileSizeInByte))
		ad.Make, ad.Model = stringOf(e.Make), stringOf(e.Model)
		ad.Lens = stringOf(e.LensModel)
		ad.FNumber = floatOf(e.FNumber)
		ad.Exposure = stringOf(e.ExposureTime)
		ad.ISO = intOf(e.Iso)
		ad.Focal = floatOf(e.FocalLength)
		ad.City, ad.State, ad.Country = stringOf(e.City), stringOf(e.State), stringOf(e.Country)
		ad.Latitude, ad.Longitude = e.Latitude, e.Longitude
		ad.Description = stringOf(e.Description)
		// The EXIF dimensions are the camera's own; the asset's may be the
		// preview's.
		if w := intOf(e.ExifImageWidth); w > 0 {
			ad.Width = w
		}
		if h := intOf(e.ExifImageHeight); h > 0 {
			ad.Height = h
		}
	}
	return ad, nil
}

// stringOf is the string an optional response field holds, or "".
func stringOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// intOf is the int an optional response field holds, or 0.
func intOf(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// floatOf is the float an optional response field holds, or 0.
func floatOf(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// weekdays are the days of the week as the timeline's headings show them.
var weekdays = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// bucketTitle is the heading of a day of the timeline: its date, or the
// month or the year of a coarser bucket, and the day of the week of a day.
func bucketTitle(bucket string, now time.Time) (title, week string) {
	if t, err := time.Parse("2006-01-02", bucket); err == nil {
		if t.Year() == now.Year() {
			title = t.Format("1月2日")
		} else {
			title = t.Format("2006年1月2日")
		}
		return title, weekdays[t.Weekday()]
	}
	if t, err := time.Parse("2006-01", bucket); err == nil {
		return t.Format("2006年1月"), ""
	}
	if t, err := time.Parse("2006", bucket); err == nil {
		return t.Format("2006年"), ""
	}
	return bucket, ""
}

// durationLabel is how long a video runs, as a badge tells it: minutes and
// seconds, and hours where they are.
func durationLabel(ms int) string {
	s := max(ms, 0) / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
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
