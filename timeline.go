package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
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

// photo is an asset of the timeline, and its thumbnail once it loads. Only
// the photos the timeline shows load theirs; thumbCache may let the ones
// scrolled away go.
type photo struct {
	ID    string
	Video bool
	// Favorite tells that the user marked the asset as a favorite.
	Favorite bool
	// Live tells a live photo: a still with a video of its own, which the
	// timeline marks as one; LiveVideo is the ID of that video.
	Live      bool
	LiveVideo string
	// Duration is how long a video runs, in milliseconds.
	Duration int
	Bm       *ui.Bitmap
	// loading tells a thumbnail is on its way; failed that it did not come,
	// so that the tile does not ask again.
	loading bool
	failed  bool
}

// assetAlbum is an album that holds the asset of the image page: what the
// details panel lists, and the thumbnail of its cover once it loads.
type assetAlbum struct {
	ID    string
	Name  string
	Count int
	// Cover is the asset whose thumbnail stands for the album, or "".
	Cover string
	Bm    *ui.Bitmap
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
	// Albums are the albums that hold the asset.
	Albums []assetAlbum
	// Duration is how long a video runs, in milliseconds.
	Duration int
}

// album is an album of the server, and the thumbnail of its cover once it
// loads.
type album struct {
	ID    string
	Name  string
	Count int
	// Latest is when the album's newest asset was taken, zero for an album
	// without assets.
	Latest time.Time
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
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到时间线，请确认服务器地址与版本"); err != nil {
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
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到这一天的照片，请刷新时间线"); err != nil {
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
		if i < len(d.IsFavorite) {
			p.Favorite = d.IsFavorite[i]
		}
		if i < len(d.Duration) && d.Duration[i] != nil {
			p.Duration = *d.Duration[i]
		}
		if i < len(d.LivePhotoVideoId) && d.LivePhotoVideoId[i] != nil &&
			*d.LivePhotoVideoId[i] != "" {
			p.Live = true
			p.LiveVideo = *d.LivePhotoVideoId[i]
		}
		photos = append(photos, p)
	}
	return photos, nil
}

// searchPageSize is how many results a search asks for at a time, and
// searchTimeout how long it may wait: a server that reads its images to
// search them takes longer than a check allows.
const (
	searchPageSize = 100
	searchTimeout  = 30 * time.Second
)

// The ways a search reads its words: by the meaning of images the server's
// machine learning learned, by the file names, or by the text OCR read of
// them.
const (
	searchSmart = iota
	searchFileName
	searchOCR
)

// searchNoAlbum is the album condition of the assets that no album holds,
// in the album row's choice and in a search request.
const searchNoAlbum = "-"

// searchRequest is what a search asks the server for: its words, the way
// they are searched, and the filter conditions chosen.
type searchRequest struct {
	text      string
	mode      int
	mediaType int
	favorite  int
	// album is the album the search asks for: "" for all, searchNoAlbum for
	// the assets no album holds, or an album ID.
	album string
}

// empty tells that a search asks for nothing: no words and no condition.
func (r searchRequest) empty() bool {
	return strings.TrimSpace(r.text) == "" && r.mediaType == 0 && r.favorite == 0 && r.album == ""
}

// fetchClipEnabled tells whether the server searches images by meaning, as
// its machine learning does; without it, a search reads file names.
func fetchClipEnabled(base, token string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return false, err
	}
	resp, err := client.GetUserConfigWithResponse(ctx)
	if err != nil {
		return false, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status(), "无法读取服务器配置"); err != nil {
		return false, err
	}
	if resp.JSON200 == nil {
		return false, errors.New("服务器没有返回配置")
	}
	return resp.JSON200.MachineLearning.Clip.Enabled, nil
}

// fetchSearchPage returns a page of the results of a search, page 1 first,
// with how many assets matched in all and the page after it, 0 at the end.
func fetchSearchPage(base, token string, req searchRequest, page int) (photos []*photo, total, next int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
	defer cancel()

	client, err := immichClientTimeout(base, token, searchTimeout)
	if err != nil {
		return nil, 0, 0, err
	}
	typ, favorite, visibility := searchParams(req)
	albumIDs, notInAlbum := searchAlbumParams(req)
	size := searchPageSize
	if req.mode == searchSmart && req.text != "" {
		query := req.text
		resp, err := client.SearchSmartWithResponse(ctx, immichapi.SmartSearchDto{
			Query:        &query,
			Page:         &page,
			Size:         &size,
			Type:         typ,
			IsFavorite:   favorite,
			Visibility:   visibility,
			AlbumIds:     albumIDs,
			IsNotInAlbum: notInAlbum,
		})
		if err != nil {
			return nil, 0, 0, requestError(err)
		}
		if resp.StatusCode() == http.StatusBadRequest {
			return nil, 0, 0, errors.New("服务器未开启智能搜索，请改用文件名或 OCR 搜索")
		}
		if err := statusErr(resp.StatusCode(), resp.Status(), "没有找到搜索结果，请重试"); err != nil {
			return nil, 0, 0, err
		}
		return searchPageAssets(resp.JSON200)
	}

	dto := immichapi.MetadataSearchDto{
		Page:         &page,
		Size:         &size,
		Type:         typ,
		IsFavorite:   favorite,
		Visibility:   visibility,
		AlbumIds:     albumIDs,
		IsNotInAlbum: notInAlbum,
	}
	order := immichapi.AssetOrderDesc
	dto.Order = &order
	if req.text != "" {
		switch req.mode {
		case searchFileName:
			dto.OriginalFileName = &req.text
		case searchOCR:
			dto.Ocr = &req.text
		}
	}
	resp, err := client.SearchAssetsWithResponse(ctx, &immichapi.SearchAssetsParams{}, dto)
	if err != nil {
		return nil, 0, 0, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status(), "没有找到搜索结果，请重试"); err != nil {
		return nil, 0, 0, err
	}
	return searchPageAssets(resp.JSON200)
}

// searchParams is what a search asks beside its words: an asset type and a
// favorite mark, both left out when the condition is all, and the timeline's
// visibility.
func searchParams(req searchRequest) (typ *immichapi.AssetTypeEnum, favorite *bool, visibility *immichapi.AssetVisibility) {
	switch req.mediaType {
	case 1:
		t := immichapi.IMAGE
		typ = &t
	case 2:
		t := immichapi.VIDEO
		typ = &t
	}
	switch req.favorite {
	case 1:
		f := true
		favorite = &f
	case 2:
		f := false
		favorite = &f
	}
	visible := immichapi.Timeline
	return typ, favorite, &visible
}

// searchAlbumParams is the album condition of a search: the ID of the album
// it asks for, or that it asks for the assets no album holds. Both are left
// out when the condition is all.
func searchAlbumParams(req searchRequest) (ids *[]uuid.UUID, notInAlbum *bool) {
	switch req.album {
	case "":
		return nil, nil
	case searchNoAlbum:
		no := true
		return nil, &no
	}
	id, err := uuid.Parse(req.album)
	if err != nil {
		return nil, nil
	}
	return &[]uuid.UUID{id}, nil
}

// searchPageAssets turns a page of the server's search into the photos of
// the timeline, and reads where the page after it begins.
func searchPageAssets(dto *immichapi.SearchResponseDto) (photos []*photo, total, next int, err error) {
	if dto == nil {
		return nil, 0, 0, errors.New("服务器没有返回搜索结果")
	}
	assets := dto.Assets
	photos = make([]*photo, 0, len(assets.Items))
	for _, d := range assets.Items {
		p := &photo{
			ID:       d.Id.String(),
			Video:    d.Type == immichapi.VIDEO,
			Favorite: d.IsFavorite,
			Duration: intOf(d.Duration),
		}
		if id := stringOf(d.LivePhotoVideoId); id != "" {
			p.Live = true
			p.LiveVideo = id
		}
		photos = append(photos, p)
	}
	if assets.NextPage != nil {
		if n, err := strconv.Atoi(*assets.NextPage); err == nil && n > 0 {
			next = n
		}
	}
	return photos, assets.Total, next, nil
}

// appendSearchPhotos appends the photos of the next page that the results do
// not hold yet, and tells how many it added: a page that adds none repeats
// the ones shown, and ends the search.
func appendSearchPhotos(d *timelineDay, photos []*photo) int {
	seen := make(map[string]struct{}, len(d.photos))
	for _, p := range d.photos {
		seen[p.ID] = struct{}{}
	}
	added := 0
	for _, p := range photos {
		if _, ok := seen[p.ID]; ok {
			continue
		}
		seen[p.ID] = struct{}{}
		d.photos = append(d.photos, p)
		added++
	}
	return added
}

// fetchAlbums returns the user's albums, those whose newest asset is the
// most recent first.
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
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到相册，请刷新"); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errors.New("服务器没有返回相册")
	}

	albums := make([]*album, 0, len(*resp.JSON200))
	for _, d := range *resp.JSON200 {
		a := &album{ID: d.Id.String(), Name: d.AlbumName, Count: d.AssetCount}
		if d.EndDate != nil {
			a.Latest = *d.EndDate
		}
		if d.AlbumThumbnailAssetId != nil {
			a.Cover = d.AlbumThumbnailAssetId.String()
		}
		albums = append(albums, a)
	}
	// The newest assets lead; albums without any keep the server's order
	// at the end.
	sort.SliceStable(albums, func(i, j int) bool {
		return albums[i].Latest.After(albums[j].Latest)
	})
	return albums, nil
}

// fetchAvatar returns the profile image of a user.
func fetchAvatar(base, token, id string) (*ui.Bitmap, error) {
	userID, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, err
	}
	resp, err := client.GetProfileImageWithResponse(ctx, userID)
	if err != nil {
		return nil, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status(), "没有找到头像"); err != nil {
		return nil, err
	}
	bm, err := ui.DecodeBitmap(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("无法读取头像：%v", err)
	}
	return bm, nil
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
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到"+what+"，可能已被删除，或当前账号无权访问"); err != nil {
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
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到这个资源，可能已被删除，或当前账号无权访问"); err != nil {
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
	// The albums that hold the asset, and their covers; a failure leaves
	// them out rather than hiding the details.
	if albums, err := client.GetAllAlbumsWithResponse(ctx,
		&immichapi.GetAllAlbumsParams{AssetId: &assetID}); err == nil && albums.JSON200 != nil {
		for _, al := range *albums.JSON200 {
			aa := assetAlbum{ID: al.Id.String(), Name: al.AlbumName, Count: al.AssetCount}
			if al.AlbumThumbnailAssetId != nil {
				aa.Cover = al.AlbumThumbnailAssetId.String()
			}
			ad.Albums = append(ad.Albums, aa)
		}
	}
	for i := range ad.Albums {
		if cover := ad.Albums[i].Cover; cover != "" {
			if bm, err := fetchThumbnail(base, token, cover); err == nil {
				ad.Albums[i].Bm = bm
			}
		}
	}
	return ad, nil
}

// setAssetFavorite marks an asset as a favorite, or takes the mark away,
// and returns the mark the server keeps.
func setAssetFavorite(base, token, id string, favorite bool) (bool, error) {
	assetID, err := uuid.Parse(id)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return false, err
	}
	resp, err := client.UpdateAssetWithResponse(ctx, assetID,
		immichapi.UpdateAssetDto{IsFavorite: &favorite})
	if err != nil {
		return false, requestError(err)
	}
	if err := statusErr(resp.StatusCode(), resp.Status(),
		"没有找到这个资源，可能已被删除，或当前账号无权访问"); err != nil {
		return false, err
	}
	if resp.JSON200 != nil {
		return resp.JSON200.IsFavorite, nil
	}
	return favorite, nil
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
// is errSessionExpired, and a 404 means the server found nothing, which
// missing tells for the request that asked.
func statusErr(code int, status, missing string) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return errSessionExpired
	case code == http.StatusNotFound:
		return errors.New(missing)
	default:
		return fmt.Errorf("服务器返回了 %s", status)
	}
}
