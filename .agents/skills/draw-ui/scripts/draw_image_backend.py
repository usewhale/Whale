#!/usr/bin/env python3
from __future__ import annotations

import base64
import fnmatch
import http.client
import ipaddress
import json
import mimetypes
import os
import platform
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any


DEFAULT_BASE_URL = "https://image.speedpony.xyz/v1"
DEFAULT_MODEL = "gpt-image-2"
DEFAULT_SIZE = "auto"
DEFAULT_MODERATION = "auto"
DEFAULT_OUTPUT_FORMAT = "png"
PROMPT_GUARD = "Use the following text as the complete prompt. Do not rewrite it:"
USER_AGENT = (
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) "
    "AppleWebKit/537.36 (KHTML, like Gecko) "
    "Chrome/147.0.0.0 Safari/537.36"
)
PROXY_ENV_VARS = ("http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY")
NO_PROXY_ENV_VARS = ("no_proxy", "NO_PROXY")


class ImageBackendError(Exception):
    pass


class ApiHttpError(ImageBackendError):
    def __init__(self, code: int, body: str):
        self.code = code
        self.body = body
        super().__init__(f"HTTP {code}: {body}")


@dataclass(frozen=True)
class SystemProxyConfig:
    proxies: dict[str, str]
    exceptions: tuple[str, ...]


class DrawImageBackend:
    def __init__(
        self,
        *,
        api_key: str,
        base_url: str | None = None,
        model: str | None = None,
        timeout: int | None = None,
        retry_count: int | None = None,
        retry_delay: int | None = None,
        origin: str | None = None,
        referer: str | None = None,
    ) -> None:
        self.api_key = api_key.strip()
        self.base_url = normalize_base_url(base_url or os.getenv("DRAW_BASE_URL") or DEFAULT_BASE_URL)
        self.model = model or os.getenv("DRAW_MODEL") or DEFAULT_MODEL
        self.timeout = timeout if timeout is not None else int(os.getenv("DRAW_TIMEOUT", "600"))
        self.retry_count = retry_count if retry_count is not None else int(os.getenv("DRAW_RETRY_COUNT", "3"))
        self.retry_delay = retry_delay if retry_delay is not None else int(os.getenv("DRAW_RETRY_DELAY", "2"))
        self.origin = origin or os.getenv("DRAW_ORIGIN", "https://image.aicodelink.top")
        self.referer = referer or os.getenv("DRAW_REFERER", "https://image.aicodelink.top/")
        self.size = os.getenv("DRAW_SIZE", DEFAULT_SIZE)
        self.moderation = os.getenv("DRAW_MODERATION", DEFAULT_MODERATION)
        self.output_format = os.getenv("DRAW_OUTPUT_FORMAT", DEFAULT_OUTPUT_FORMAT)
        self.quality = os.getenv("DRAW_QUALITY", "").strip()
        self.system_proxy = resolve_system_proxy_config()
        self.opener = build_url_opener(self.system_proxy)
        self.direct_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def generate(self, *, prompt: str, output_path: Path) -> None:
        payload = self._build_common_payload(prompt)
        result = self._post_json(self._endpoint("images/generations"), payload)
        self._save_first_image(result, output_path)

    def edit(self, *, prompt: str, refs: list[Path], output_path: Path) -> None:
        if not refs:
            raise ImageBackendError("edit mode requires at least one reference image.")

        fields = self._build_common_payload(prompt)
        files: list[tuple[str, str, bytes]] = []
        for ref in refs:
            with ref.open("rb") as handle:
                files.append(("image[]", ref.name, handle.read()))

        result = self._post_multipart(self._endpoint("images/edits"), fields, files)
        self._save_first_image(result, output_path)

    def _build_common_payload(self, prompt: str) -> dict[str, str]:
        payload = {
            "model": self.model,
            "prompt": guarded_prompt(prompt),
            "size": self.size,
            "moderation": self.moderation,
            "output_format": self.output_format,
        }
        if self.quality:
            payload["quality"] = self.quality
        return payload

    def _endpoint(self, path: str) -> str:
        return f"{self.base_url}/{path.lstrip('/')}"

    def _headers(self, content_type: str | None = None) -> dict[str, str]:
        headers = {
            "Accept": "*/*",
            "Authorization": f"Bearer {self.api_key}",
            "Cache-Control": "no-store, no-cache, max-age=0",
            "Origin": self.origin,
            "Pragma": "no-cache",
            "Referer": self.referer,
            "User-Agent": USER_AGENT,
        }
        if content_type:
            headers["Content-Type"] = content_type
        return headers

    def _post_json(self, url: str, payload: dict[str, str]) -> dict[str, Any]:
        def action() -> dict[str, Any]:
            request = urllib.request.Request(
                url=url,
                data=json.dumps(payload).encode("utf-8"),
                method="POST",
                headers=self._headers("application/json"),
            )
            try:
                with self._open(request, timeout=self.timeout) as response:
                    return json.loads(response.read().decode("utf-8"))
            except urllib.error.HTTPError as exc:
                raise self._http_error(exc)

        return self._with_retry(action, f"POST {url}")

    def _post_multipart(self, url: str, fields: dict[str, str], files: list[tuple[str, str, bytes]]) -> dict[str, Any]:
        def action() -> dict[str, Any]:
            boundary = f"----DrawUiBoundary{uuid.uuid4().hex}"
            body = bytearray()

            for name, value in fields.items():
                body.extend(f"--{boundary}\r\n".encode())
                body.extend(f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode())
                body.extend(str(value).encode("utf-8"))
                body.extend(b"\r\n")

            for field_name, filename, content in files:
                mime_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
                body.extend(f"--{boundary}\r\n".encode())
                body.extend(
                    f'Content-Disposition: form-data; name="{field_name}"; filename="{filename}"\r\n'.encode()
                )
                body.extend(f"Content-Type: {mime_type}\r\n\r\n".encode())
                body.extend(content)
                body.extend(b"\r\n")

            body.extend(f"--{boundary}--\r\n".encode())

            request = urllib.request.Request(
                url=url,
                data=bytes(body),
                method="POST",
                headers=self._headers(f"multipart/form-data; boundary={boundary}"),
            )
            try:
                with self._open(request, timeout=self.timeout) as response:
                    return json.loads(response.read().decode("utf-8"))
            except urllib.error.HTTPError as exc:
                raise self._http_error(exc)

        return self._with_retry(action, f"POST {url}")

    def _save_first_image(self, result: dict[str, Any], output_path: Path) -> None:
        data = result.get("data")
        if not isinstance(data, list) or not data:
            raise ImageBackendError(f"Image API response did not contain data[0]: {json.dumps(result)[:1000]}")

        item = data[0]
        if not isinstance(item, dict):
            raise ImageBackendError(f"Image API response data[0] was not an object: {item!r}")

        b64_image = item.get("b64_json")
        if isinstance(b64_image, str) and b64_image:
            output_path.write_bytes(base64.b64decode(b64_image))
            return

        image_url = item.get("url")
        if isinstance(image_url, str) and image_url:
            output_path.write_bytes(self._download(image_url))
            return

        raise ImageBackendError("Image API response did not contain data[0].b64_json or data[0].url.")

    def _download(self, url: str) -> bytes:
        def action() -> bytes:
            request = urllib.request.Request(url, method="GET", headers=self._headers())
            try:
                with self._open(request, timeout=self.timeout) as response:
                    return response.read()
            except urllib.error.HTTPError as exc:
                raise self._http_error(exc)

        return self._with_retry(action, f"GET {url}")

    def _open(self, request: urllib.request.Request, *, timeout: int):
        if self.system_proxy and should_bypass_system_proxy(request.full_url, self.system_proxy.exceptions):
            return self.direct_opener.open(request, timeout=timeout)
        return self.opener.open(request, timeout=timeout)

    def _http_error(self, exc: urllib.error.HTTPError) -> ApiHttpError:
        body = exc.read().decode("utf-8", errors="replace")
        return ApiHttpError(exc.code, body)

    def _with_retry(self, action, description: str):
        last_exc: Exception | None = None
        for attempt in range(1, self.retry_count + 1):
            try:
                return action()
            except Exception as exc:
                last_exc = exc
                if not should_retry(exc) or attempt >= self.retry_count:
                    raise
                delay = self.retry_delay * attempt
                print(
                    f"[WARN] {description} failed on attempt {attempt}/{self.retry_count}: {exc}. "
                    f"retrying in {delay}s...",
                    file=sys.stderr,
                )
                time.sleep(delay)

        if last_exc is not None:
            raise last_exc
        raise ImageBackendError(f"{description} failed without an exception.")


def guarded_prompt(prompt: str) -> str:
    prompt = prompt.strip()
    if prompt.startswith(PROMPT_GUARD):
        return prompt
    return f"{PROMPT_GUARD}\n{prompt}"


def normalize_base_url(value: str) -> str:
    value = value.strip().rstrip("/")
    if not value:
        return DEFAULT_BASE_URL
    return value


def build_url_opener(system_proxy: SystemProxyConfig | None) -> urllib.request.OpenerDirector:
    if has_explicit_proxy_env():
        return urllib.request.build_opener()
    if system_proxy:
        return urllib.request.build_opener(urllib.request.ProxyHandler(system_proxy.proxies))
    return urllib.request.build_opener()


def has_explicit_proxy_env() -> bool:
    return any(os.getenv(name) for name in (*PROXY_ENV_VARS, *NO_PROXY_ENV_VARS))


def resolve_system_proxy_config() -> SystemProxyConfig | None:
    if platform.system() != "Darwin" or has_explicit_proxy_env():
        return None

    try:
        completed = subprocess.run(
            ["scutil", "--proxy"],
            capture_output=True,
            check=False,
            text=True,
            timeout=3,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        print(f"[WARN] Could not read macOS system proxy settings: {exc}", file=sys.stderr)
        return None

    if completed.returncode != 0:
        detail = completed.stderr.strip() or f"exit code {completed.returncode}"
        print(f"[WARN] Could not read macOS system proxy settings: {detail}", file=sys.stderr)
        return None

    config = parse_scutil_proxy_output(completed.stdout)
    if not config or not config.proxies:
        return None
    return config


def parse_scutil_proxy_output(output: str) -> SystemProxyConfig | None:
    values = parse_scutil_values(output)
    exceptions = parse_scutil_exceptions(output)
    proxies: dict[str, str] = {}

    if values.get("HTTPEnable") == "1":
        proxy = build_proxy_url("http", values.get("HTTPProxy"), values.get("HTTPPort"))
        if proxy:
            proxies["http"] = proxy

    if values.get("HTTPSEnable") == "1":
        proxy = build_proxy_url("http", values.get("HTTPSProxy"), values.get("HTTPSPort"))
        if proxy:
            proxies["https"] = proxy

    if values.get("SOCKSEnable") == "1" and not proxies:
        print(
            "[WARN] macOS SOCKS proxy is enabled, but draw-ui only auto-applies system HTTP/HTTPS proxies.",
            file=sys.stderr,
        )

    if not proxies:
        return None
    return SystemProxyConfig(proxies=proxies, exceptions=tuple(exceptions))


def parse_scutil_values(output: str) -> dict[str, str]:
    values: dict[str, str] = {}
    for line in output.splitlines():
        stripped = line.strip()
        if " : " not in stripped:
            continue
        key, value = stripped.split(" : ", 1)
        if key and not key.isdigit():
            values[key] = value.strip()
    return values


def parse_scutil_exceptions(output: str) -> list[str]:
    exceptions: list[str] = []
    in_exceptions = False
    for line in output.splitlines():
        stripped = line.strip()
        if stripped.startswith("ExceptionsList :"):
            in_exceptions = True
            continue
        if not in_exceptions:
            continue
        if stripped == "}":
            break
        if " : " not in stripped:
            continue
        key, value = stripped.split(" : ", 1)
        if key.isdigit() and value.strip():
            exceptions.append(value.strip())
    return exceptions


def build_proxy_url(scheme: str, host: str | None, port: str | None) -> str | None:
    if not host or not port:
        return None
    try:
        parsed_port = int(port)
    except ValueError:
        return None
    if parsed_port <= 0:
        return None
    return f"{scheme}://{host}:{parsed_port}"


def should_bypass_system_proxy(url: str, exceptions: tuple[str, ...]) -> bool:
    if not exceptions:
        return False

    hostname = urllib.parse.urlparse(url).hostname
    if not hostname:
        return False

    host = hostname.strip("[]").lower()
    for pattern in exceptions:
        if proxy_exception_matches(host, pattern):
            return True
    return False


def proxy_exception_matches(host: str, pattern: str) -> bool:
    pattern = pattern.strip().lower()
    if not pattern:
        return False
    if pattern == "<local>":
        return "." not in host
    if pattern.startswith("*."):
        suffix = pattern[1:]
        return host.endswith(suffix)
    if any(char in pattern for char in "*?"):
        return fnmatch.fnmatch(host, pattern)
    if "/" in pattern:
        return host_in_network(host, pattern)
    return host == pattern or host.endswith(f".{pattern}")


def host_in_network(host: str, pattern: str) -> bool:
    try:
        network = ipaddress.ip_network(pattern, strict=False)
        address = ipaddress.ip_address(host)
    except ValueError:
        return False
    return address in network


def should_retry(exc: Exception) -> bool:
    if isinstance(exc, ApiHttpError):
        return exc.code == 429 or exc.code >= 500
    if isinstance(exc, urllib.error.HTTPError):
        return exc.code == 429 or exc.code >= 500
    if isinstance(exc, (urllib.error.URLError, TimeoutError, socket.timeout, http.client.RemoteDisconnected)):
        return True
    return False
