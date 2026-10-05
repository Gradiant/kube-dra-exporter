"""Check shipped chart/image versions and optional Git release tags before publishing."""
import argparse
from pathlib import Path
import re
import sys

import yaml

SEMVER = re.compile(
    r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)'
    r'(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?'
    r'(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?'
)


def version(value, field, image=False):
    if not isinstance(value, str):
        raise ValueError(f'{field} must be a semantic version string')
    match = SEMVER.fullmatch(value)
    if not match:
        raise ValueError(f'{field} must be a semantic version, got {value!r}')
    for identifier in (match.group(4) or '').split('.'):
        if identifier.isdigit() and len(identifier) > 1 and identifier.startswith('0'):
            raise ValueError(f'{field} has a prerelease number with leading zeros')
    if image and ('+' in value or len(value) > 128):
        raise ValueError(f'{field} must also be a valid Docker image tag (no build metadata, at most 128 characters)')
    return value


def check_release(chart_dir, release_tag=None):
    chart = yaml.safe_load((chart_dir / 'Chart.yaml').read_text())
    chart_version = version(chart.get('version'), 'Chart.yaml version')
    app_version = version(chart.get('appVersion'), 'Chart.yaml appVersion', image=True)
    if chart_version != app_version:
        raise ValueError('Chart.yaml version and appVersion must match for a unified image/chart release')
    default_values = yaml.safe_load((chart_dir / 'values.yaml').read_text())
    default_image = default_values.get('image') or {}
    profiles = [('values.yaml', default_image)]
    for path in sorted(chart_dir.glob('*values.yaml')):
        if path.name != 'values.yaml':
            values = yaml.safe_load(path.read_text()) or {}
            profiles.append((path.name, {**default_image, **(values.get('image') or {})}))
    for filename, image in profiles:
        tag = image.get('tag')
        if tag not in (None, '', app_version):
            raise ValueError(f'{filename} image.tag={tag!r} differs from appVersion={app_version!r}; leave it empty to follow appVersion')
        if image.get('digest'):
            raise ValueError(f'{filename} image.digest pins an image independently of appVersion; remove it from shipped release defaults')
    if release_tag is not None:
        tagged_version = version(release_tag.removeprefix('v'), 'release tag', image=True)
        if tagged_version != chart_version:
            raise ValueError(f'{release_tag} does not match Chart.yaml version/appVersion={chart_version!r}')
    return chart_version, app_version


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--chart', type=Path, default=Path(__file__).resolve().parents[1] / 'deploy/helm/kube-dra-exporter')
    parser.add_argument('--release-tag')
    args = parser.parse_args()
    try:
        chart_version, app_version = check_release(args.chart, args.release_tag)
    except (ValueError, KeyError, TypeError, OSError, yaml.YAMLError) as error:
        print(f'Release consistency check failed: {error}', file=sys.stderr)
        return 1
    print(f'Release consistency OK: chart={chart_version}, app/image={app_version}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
