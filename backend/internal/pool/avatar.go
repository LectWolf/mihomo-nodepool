package pool

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
)

const MaxAvatarBytes = 200 << 10

// 头像版本为正数表示存在头像，删除后取负值保留计数以避免缓存旧图
// SetAvatar stores a small static avatar; GIF/APNG are flattened to their first frame.
func (s *Store) SetAvatar(ctx context.Context, data []byte) (User, error) {
	if len(data) == 0 || len(data) > MaxAvatarBytes {
		return User{}, errors.New("头像须为 200KB 以内的图片")
	}
	data, kind, err := staticAvatar(data)
	if err != nil {
		return User{}, err
	}
	id := CurrentUser(ctx).ID
	if _, err = s.DB.ExecContext(ctx, `UPDATE np_users SET avatar=?,avatar_type=?,avatar_version=ABS(avatar_version)+1 WHERE id=?`, data, kind, id); err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}

func (s *Store) DeleteAvatar(ctx context.Context) (User, error) {
	id := CurrentUser(ctx).ID
	if _, err := s.DB.ExecContext(ctx, `UPDATE np_users SET avatar=NULL,avatar_type='',avatar_version=-ABS(avatar_version) WHERE id=?`, id); err != nil {
		return User{}, err
	}
	return s.User(ctx, id)
}

func (s *Store) Avatar(ctx context.Context, id int64) ([]byte, string, error) {
	var data []byte
	var kind string
	err := s.DB.QueryRowContext(ctx, `SELECT avatar,avatar_type FROM np_users WHERE id=? AND enabled=1 AND avatar IS NOT NULL`, id).Scan(&data, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	return data, kind, err
}

// staticAvatar 校验头像并去除动画：JPEG 原样保存，PNG/GIF 重新编码为静态首帧
func staticAvatar(data []byte) ([]byte, string, error) {
	kind := http.DetectContentType(data)
	if kind != "image/png" && kind != "image/jpeg" && kind != "image/gif" {
		return nil, "", errors.New("头像仅支持 PNG、JPEG 或 GIF")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 16 || cfg.Height < 16 || cfg.Width > 1024 || cfg.Height > 1024 {
		return nil, "", errors.New("头像尺寸须为 16–1024 像素")
	}
	if kind == "image/jpeg" {
		return data, kind, nil
	}
	// image/png 只解码默认图像，APNG 的后续帧会被丢弃；GIF 取第一帧
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errors.New("无法解析头像图片")
	}
	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err = enc.Encode(&out, img); err == nil && out.Len() <= MaxAvatarBytes {
		return out.Bytes(), "image/png", nil
	}
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
	for _, quality := range []int{90, 80, 65, 50} {
		out.Reset()
		if err = jpeg.Encode(&out, flat, &jpeg.Options{Quality: quality}); err == nil && out.Len() <= MaxAvatarBytes {
			return out.Bytes(), "image/jpeg", nil
		}
	}
	return nil, "", errors.New("头像转换后仍超过 200KB，请换一张图片")
}
