package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	expectedRepositoryID = 1416491919
	repositoryAPI        = "https://api.github.com/repos/"
	maxReleaseMetadata   = 256 * 1024
)

var (
	repositoryFullName = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9_.-]+$`)
	assetDigest        = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	errReleaseMetadata = errors.New("invalid GitHub release metadata")
)

type releaseAsset struct {
	url    string
	digest string
}

func (asset releaseAsset) download(ctx context.Context, limit int64, progress *upgradeProgress) ([]byte, error) {
	data, err := download(ctx, nil, asset.url, githubAsset, limit, progress)
	if err != nil {
		return nil, err
	}
	if asset.digest != "" && fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != asset.digest {
		return nil, errors.New("release asset digest mismatch")
	}
	return data, nil
}

func readReleaseObject(ctx context.Context, endpoint string, progress *upgradeProgress) (map[string]any, error) {
	data, err := download(ctx, nil, endpoint, githubMetadata, maxReleaseMetadata, progress)
	if err != nil {
		return nil, err
	}
	// Reuse the strict JSON decoder with a map to match exact field names and
	// decimal IDs while permitting GitHub to add unrelated fields.
	var object map[string]any
	if err := interactionJSON(data, &object); err != nil || object == nil {
		return nil, errReleaseMetadata
	}
	return object, nil
}

func githubID(object map[string]any) (int64, bool) {
	number, ok := object["id"].(json.Number)
	if !ok {
		return 0, false
	}
	id, err := number.Int64()
	return id, err == nil && id > 0
}

func officialRepository(ctx context.Context, locator string, progress *upgradeProgress) (string, error) {
	repo, err := readReleaseObject(ctx, repositoryAPI+locator, progress)
	if errors.Is(err, errReleaseNotFound) {
		return "", errors.New("official repository unavailable")
	}
	if err != nil {
		return "", err
	}
	id, ok := githubID(repo)
	fullName, _ := repo["full_name"].(string)
	if !ok || !repositoryFullName.MatchString(fullName) || strings.HasSuffix(fullName, "/.") || strings.HasSuffix(fullName, "/..") {
		return "", errReleaseMetadata
	}
	if id != expectedRepositoryID {
		return "", errReleaseSource
	}
	return fullName, nil
}

func officialReleaseAssets(ctx context.Context, version string, progress *upgradeProgress) (map[string]releaseAsset, error) {
	fullName, err := officialRepository(ctx, releaseRepo, progress)
	if err != nil {
		return nil, err
	}
	base := repositoryAPI + fullName + "/releases/"
	release, err := readReleaseObject(ctx, base+"tags/"+version, progress)
	if err != nil && !errors.Is(err, errReleaseNotFound) {
		return nil, err
	}
	// A named route can change owners between the repository and release reads.
	// Recheck the exact name used for this lookup before accepting its result.
	currentName, checkErr := officialRepository(ctx, fullName, progress)
	if checkErr != nil {
		return nil, checkErr
	}
	if currentName != fullName {
		return nil, errReleaseSource
	}
	if errors.Is(err, errReleaseNotFound) {
		return nil, errors.New("requested release unavailable")
	}
	return parseReleaseAssets(release, base, version)
}

func parseReleaseAssets(release map[string]any, base, version string) (map[string]releaseAsset, error) {
	id, ok := githubID(release)
	draft, hasDraft := release["draft"].(bool)
	prerelease, hasPrerelease := release["prerelease"].(bool)
	items, hasAssets := release["assets"].([]any)
	tag, hasTag := release["tag_name"].(string)
	if !ok || !hasDraft || !hasPrerelease || !hasAssets || !hasTag {
		return nil, errReleaseMetadata
	}
	if release["url"] != base+strconv.FormatInt(id, 10) {
		return nil, errReleaseSource
	}
	if draft || prerelease || tag != version {
		return nil, errors.New("requested stable release unavailable")
	}

	assets := make(map[string]releaseAsset, len(items))
	ids := make(map[int64]bool, len(items))
	for _, item := range items {
		object, _ := item.(map[string]any)
		assetID, ok := githubID(object)
		name, _ := object["name"].(string)
		endpoint, _ := object["url"].(string)
		if !ok || name == "" {
			return nil, errReleaseMetadata
		}
		if endpoint != base+"assets/"+strconv.FormatInt(assetID, 10) {
			return nil, errReleaseSource
		}
		if _, exists := assets[name]; exists || ids[assetID] {
			return nil, errors.New("duplicate release asset")
		}
		digest, _ := object["digest"].(string)
		if object["digest"] != nil && !assetDigest.MatchString(digest) {
			return nil, errors.New("invalid release asset digest")
		}
		assets[name] = releaseAsset{url: endpoint, digest: digest}
		ids[assetID] = true
	}

	for _, platform := range []string{"linux_amd64", "linux_arm64", "darwin_amd64", "darwin_arm64"} {
		if _, ok := assets["mobterm-cli_"+version+"_"+platform+".tar.gz"]; !ok {
			return nil, errors.New("release is incomplete")
		}
	}
	if _, ok := assets["checksums.txt"]; !ok {
		return nil, errors.New("release checksum unavailable")
	}
	return assets, nil
}
