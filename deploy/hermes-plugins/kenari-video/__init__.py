"""Video generation on the profile's Kenari endpoint (https://kenari.id/docs/videos).

Kenari's video API is asynchronous: ``POST /videos/generations`` returns a job id, ``GET /videos/{id}`` reports
``rendering`` until the job is ``done`` (or ``failed`` / ``expired``, both refunded), and ``GET /videos/{id}/content``
streams the clip, for the account that made it only. The key and base URL are the profile's own (OPENAI_API_KEY and
OPENAI_BASE_URL, the same ones the chat model and the image backend use). The clip is saved to the profile's cache like
any other generated media. Model: ``video_gen.kenari-video.model`` or ``KENARI_VIDEO_MODEL``."""
from __future__ import annotations

import logging
import os
import time
from typing import Any, Optional

import httpx

from agent.secret_scope import get_secret_str
from agent.video_gen_provider import (DEFAULT_ASPECT_RATIO, DEFAULT_RESOLUTION, VideoGenProvider, error_response,
                                      save_bytes_video, success_response)

logger = logging.getLogger(__name__)

DEFAULT_MODEL = "veo-3.1-lite"
POLL_INTERVAL_S = 5.0
POLL_DEADLINE_S = 900.0
MAX_CLIP_BYTES = 200 * 1024 * 1024
_TEMPORARY = {429, 503}


def _config() -> dict[str, Any]:
    """``video_gen.kenari-video`` from the profile's config.yaml; ``{}`` on any failure."""
    try:
        from hermes_cli.config import load_config

        section = (load_config() or {}).get("video_gen")
        section = section.get("kenari-video") if isinstance(section, dict) else None
        return section if isinstance(section, dict) else {}
    except Exception:
        return {}


def _nearest(allowed: list[int], want: Optional[int]) -> Optional[int]:
    if not allowed:
        return want
    if want is None:
        return allowed[0]
    return min(allowed, key=lambda d: (abs(d - want), d))


class KenariVideoProvider(VideoGenProvider):
    name = "kenari-video"
    display_name = "Kenari"

    def is_available(self) -> bool:
        return bool(get_secret_str("OPENAI_API_KEY", "").strip() and get_secret_str("OPENAI_BASE_URL", "").strip())

    def default_model(self) -> Optional[str]:
        return (str(_config().get("model") or "").strip() or os.environ.get("KENARI_VIDEO_MODEL", "").strip()
                or DEFAULT_MODEL)

    def list_models(self) -> list[dict[str, Any]]:
        return [{"id": self.default_model(), "display": "Kenari video model", "modalities": ["text", "image"]}]

    def capabilities(self) -> dict[str, Any]:
        return {"modalities": ["text", "image"], "aspect_ratios": ["16:9", "9:16", "1:1"], "resolutions": ["360p", "720p", "1080p"],
                "max_duration": 10, "min_duration": 3, "max_reference_images": 0}

    def _limits(self, client: httpx.Client, base: str, model: str) -> tuple[list[int], list[str]]:
        """The durations and resolutions Kenari lists for the model; empty when it lists none (or cannot be read)."""
        try:
            data = client.get(f"{base}/models").json().get("data") or []
            for m in data:
                if m.get("id") == model:
                    return [int(d) for d in m.get("video_durations") or []], [str(r) for r in m.get("video_resolutions") or []]
        except Exception as exc:
            logger.debug("kenari-video: model limits unavailable: %s", exc)
        return [], []

    def generate(self, prompt: str, *, model: Optional[str] = None, image_url: Optional[str] = None,
                 reference_image_urls: Optional[list[str]] = None, duration: Optional[int] = None,
                 aspect_ratio: str = DEFAULT_ASPECT_RATIO, resolution: str = DEFAULT_RESOLUTION,
                 negative_prompt: Optional[str] = None, audio: Optional[bool] = None, seed: Optional[int] = None,
                 **kwargs: Any) -> dict[str, Any]:
        prompt = (prompt or "").strip()
        use = (model or "").strip() or self.default_model() or DEFAULT_MODEL
        fail = lambda msg, kind="provider_error": error_response(error=msg, error_type=kind, provider=self.name,
                                                                  model=use, prompt=prompt, aspect_ratio=aspect_ratio)
        if not prompt:
            return fail("prompt is required", "invalid_request")
        key, base = get_secret_str("OPENAI_API_KEY", "").strip(), get_secret_str("OPENAI_BASE_URL", "").strip().rstrip("/")
        if not key or not base:
            return fail("The Kenari key or base URL is not set", "missing_credentials")
        auth = {"Authorization": f"Bearer {key}"}
        try:
            with httpx.Client(headers=auth, timeout=60.0) as client:
                durations, resolutions = self._limits(client, base, use)
                body: dict[str, Any] = {"model": use, "prompt": prompt, "aspect_ratio": aspect_ratio}
                if (d := _nearest(durations, duration)) is not None:
                    body["duration"] = d
                if resolutions:
                    body["resolution"] = resolution if resolution in resolutions else resolutions[0]
                elif resolution:
                    body["resolution"] = resolution
                if image_url:
                    body["image_url"] = image_url
                made = client.post(f"{base}/videos/generations", json=body)
                if made.status_code >= 400:
                    return fail(f"Kenari refused the request ({made.status_code}): {made.text[:300]}", "api_error")
                job = made.json().get("id")
                if not job:
                    return fail("Kenari returned no job id", "empty_response")
                deadline = time.monotonic() + POLL_DEADLINE_S
                while True:
                    time.sleep(POLL_INTERVAL_S)
                    polled = client.get(f"{base}/videos/{job}")
                    if polled.status_code in _TEMPORARY:
                        status = "rendering"
                    elif polled.status_code >= 400:
                        return fail(f"Kenari could not report the job ({polled.status_code})", "api_error")
                    else:
                        status = str(polled.json().get("status") or "")
                    if status == "done":
                        break
                    if status in ("failed", "expired"):
                        why = str(polled.json().get("failure_reason") or status)
                        return fail(f"The video was not made ({status}): {why}. The charge is refunded.", "generation_failed")
                    if time.monotonic() >= deadline:
                        return fail("The video did not finish in time", "timeout")
                clip = client.get(f"{base}/videos/{job}/content", timeout=180.0)
                if clip.status_code >= 400 or not clip.content:
                    return fail(f"The finished clip could not be downloaded ({clip.status_code})", "download_failed")
                if len(clip.content) > MAX_CLIP_BYTES:
                    return fail("The finished clip is too large to keep", "download_failed")
        except Exception as exc:
            return fail(f"Video generation failed: {exc}", "api_error")
        ext = {"video/webm": "webm", "video/quicktime": "mov"}.get(clip.headers.get("content-type", "").split(";")[0], "mp4")
        path = save_bytes_video(clip.content, prefix="kenari", extension=ext)
        return success_response(video=str(path), model=use, prompt=prompt, modality="image" if image_url else "text",
                                aspect_ratio=aspect_ratio, duration=body.get("duration") or 0, provider=self.name)


def register(ctx) -> None:
    ctx.register_video_gen_provider(KenariVideoProvider())
