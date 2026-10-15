package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (m *Manager) backupExists() bool {
	return regularFile(m.exePath + ".backup")
}

// Resolve once before any replacement: Linux may report the renamed backup as
// os.Executable after the running binary has been moved.
func resolveExecutable(found string) (string, error) {
	if found == "" {
		var err error
		found, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("无法定位当前程序: %w", err)
		}
	}
	found, err := filepath.Abs(found)
	if err != nil {
		return "", fmt.Errorf("无法定位程序绝对路径: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(found)
	if err != nil {
		return found, nil
	}
	return resolved, nil
}

func (m *Manager) resolveAssets(rel releaseRecord) (string, string, string, error) {
	arch, err := linuxAssetArch(m.goarch)
	if err != nil {
		return "", "", "", err
	}
	exact := fmt.Sprintf("nodepool_%s_linux_%s.tar.gz", rel.Version, arch)
	suffix := fmt.Sprintf("_linux_%s.tar.gz", arch)
	var matches []ghAsset
	var checksum *ghAsset
	for _, asset := range rel.Assets {
		if asset.Name == "checksums.txt" {
			if checksum != nil {
				return "", "", "", errors.New("发布包含多个 checksums.txt")
			}
			copied := asset
			checksum = &copied
			continue
		}
		if strings.HasPrefix(asset.Name, "nodepool_") && strings.HasSuffix(asset.Name, suffix) && !strings.Contains(asset.Name, "..") {
			matches = append(matches, asset)
		}
	}
	var archive *ghAsset
	switch len(matches) {
	case 0:
		return "", "", "", fmt.Errorf("发布中没有 linux_%s 的 nodepool 压缩包", arch)
	case 1:
		archive = &matches[0]
	default:
		for i := range matches {
			if matches[i].Name == exact {
				archive = &matches[i]
				break
			}
		}
		if archive == nil {
			return "", "", "", fmt.Errorf("发布包含多个 linux_%s 压缩包", arch)
		}
	}
	if checksum == nil {
		return "", "", "", errors.New("发布中没有 checksums.txt")
	}
	archiveURL, err := m.assetURL(*archive)
	if err != nil {
		return "", "", "", err
	}
	checksumURL, err := m.assetURL(*checksum)
	if err != nil {
		return "", "", "", err
	}
	return archive.Name, archiveURL, checksumURL, nil
}

func linuxAssetArch(arch string) (string, error) {
	switch arch {
	case "amd64", "arm64":
		return arch, nil
	default:
		return "", fmt.Errorf("不支持的架构 %s", arch)
	}
}

func (m *Manager) assetURL(asset ghAsset) (string, error) {
	raw := strings.TrimSpace(asset.URL)
	if raw == "" {
		raw = strings.TrimSpace(asset.BrowserDownloadURL)
	}
	if err := m.validateDownloadURL(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func (m *Manager) applyRelease(ctx context.Context, rel releaseRecord) error {
	name, archiveURL, checksumURL, err := m.resolveAssets(rel)
	if err != nil {
		return err
	}
	exe := m.exePath
	tempDir, err := os.MkdirTemp(filepath.Dir(exe), ".nodepool-update-*")
	if err != nil {
		return fmt.Errorf("无法创建临时目录: %w", err)
	}
	defer os.RemoveAll(tempDir)
	token := m.token()
	archivePath := filepath.Join(tempDir, "archive.tar.gz")
	if err := m.downloadToFile(ctx, archiveURL, archivePath, m.maxArchive, token); err != nil {
		return fmt.Errorf("下载安装包失败: %w", err)
	}
	checksumData, err := m.fetchBytes(ctx, checksumURL, "application/octet-stream", token, maxChecksumBytes)
	if err != nil {
		return fmt.Errorf("下载校验文件失败: %w", err)
	}
	if err := verifySHA256(archivePath, name, checksumData); err != nil {
		return err
	}
	newBin := filepath.Join(tempDir, "nodepool")
	if err := extractNodepool(archivePath, newBin, m.maxBinary); err != nil {
		return err
	}
	m.setProgress(stateInstalling, "正在安装 "+rel.Version)
	return m.installBinary(exe, newBin)
}

func verifySHA256(filePath, assetName string, checksumData []byte) error {
	expected, err := checksumFor(checksumData, assetName)
	if err != nil {
		return err
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	actual := sum.Sum(nil)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return fmt.Errorf("SHA256 校验失败: 期望 %s 实际 %s", hex.EncodeToString(expected), hex.EncodeToString(actual))
	}
	return nil
}

func checksumFor(data []byte, assetName string) ([]byte, error) {
	if len(data) == 0 || len(data) > maxChecksumBytes {
		return nil, errors.New("checksums.txt 无效")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		name = strings.ReplaceAll(name, "\\", "/")
		if path.Base(name) != assetName {
			continue
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, errors.New("checksums.txt 中的哈希无效")
		}
		return sum, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("checksums.txt 缺少 %s", assetName)
}

func extractNodepool(archivePath, dest string, maxBinary int64) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("无法读取 gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	entries := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("无法读取压缩包: %w", err)
		}
		entries++
		if entries > 8 {
			return errors.New("压缩包文件数量过多")
		}
		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		}
		name := hdr.Name
		if name == "" || strings.Contains(name, "..") || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.IsAbs(name) {
			return fmt.Errorf("压缩包路径不安全: %s", name)
		}
		if name != "nodepool" {
			return fmt.Errorf("压缩包包含意外文件: %s", name)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			return errors.New("压缩包内 nodepool 不是普通文件")
		}
		if found {
			return errors.New("压缩包包含多个文件")
		}
		if hdr.Size < 0 || hdr.Size > maxBinary {
			return errors.New("程序文件过大")
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(out, io.LimitReader(tr, maxBinary+1))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n > maxBinary {
			return errors.New("程序文件过大")
		}
		if n != hdr.Size {
			return errors.New("程序文件大小与压缩包记录不一致")
		}
		found = true
	}
	if !found {
		return errors.New("压缩包中没有 nodepool")
	}
	return nil
}

func (m *Manager) installBinary(exe, newBin string) error {
	if err := os.Chmod(newBin, 0o755); err != nil {
		return fmt.Errorf("设置新程序权限失败: %w", err)
	}
	aside := exe + ".install-aside"
	_ = os.Remove(aside)
	if err := m.rename(exe, aside); err != nil {
		return fmt.Errorf("备份当前程序失败: %w", err)
	}
	if err := m.rename(newBin, exe); err != nil {
		if restoreErr := m.rename(aside, exe); restoreErr != nil {
			return fmt.Errorf("替换失败且无法恢复当前程序: %v; %w", restoreErr, err)
		}
		return fmt.Errorf("替换失败，已恢复当前程序: %w", err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return fmt.Errorf("设置程序权限失败: %w", err)
	}
	backup := exe + ".backup"
	_ = os.Remove(backup)
	if err := m.rename(aside, backup); err != nil {
		_ = os.Remove(aside)
		return fmt.Errorf("程序已替换，但保存备份失败: %w", err)
	}
	return nil
}

// restoreBackup puts the last *.backup back in place. It does not read or change a database.
func (m *Manager) restoreBackup(exe string) error {
	backup := exe + ".backup"
	if !regularFile(backup) {
		return errors.New("没有可恢复的本地备份")
	}
	aside := exe + ".rollback-aside"
	_ = os.Remove(aside)
	if err := m.rename(exe, aside); err != nil {
		return fmt.Errorf("无法移开当前程序: %w", err)
	}
	if err := m.rename(backup, exe); err != nil {
		if restoreErr := m.rename(aside, exe); restoreErr != nil {
			return fmt.Errorf("回退失败且无法恢复当前程序: %v; %w", restoreErr, err)
		}
		return fmt.Errorf("回退失败，已恢复当前程序: %w", err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return fmt.Errorf("设置程序权限失败: %w", err)
	}
	_ = os.Remove(aside)
	return nil
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
