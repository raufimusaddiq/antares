"""Image generation on the profile's OpenAI-compatible endpoint (Antares deployment: Kenari).

Hermes' bundled ``openai`` backend omits ``response_format`` (OpenAI's gpt-image rejects it), and Kenari then answers
with a ``data:`` URL, which Hermes will not fetch. This backend asks for ``b64_json``, so the image is saved to the
profile's own cache like any other. Model: ``image_gen.kenari-image.model`` or ``KENARI_IMAGE_MODEL``."""
from __future__ import annotations

import os
from typing import Any, Optional

from agent.image_gen_provider import ImageGenProvider, resolve_aspect_ratio, success_response
from agent.secret_scope import get_secret_str
from plugins.image_gen._common import (error_factory, import_openai, load_image_gen_config, materialize_image,
                                       prompt_required_error, size_for)

DEFAULT_MODEL = "agnes-image-2-5-flash:free"


class KenariImageProvider(ImageGenProvider):
    name = "kenari-image"
    display_name = "Kenari"

    def is_available(self) -> bool:
        return bool(get_secret_str("OPENAI_API_KEY", "").strip() and get_secret_str("OPENAI_BASE_URL", "").strip())

    def capabilities(self) -> dict[str, Any]:
        return {"modalities": ["text"], "max_reference_images": 0}

    def generate(self, prompt: str, aspect_ratio: str = "square", *, image_url: Optional[str] = None,
                 reference_image_urls: Optional[list[str]] = None, **kwargs: Any) -> dict[str, Any]:
        prompt = (prompt or "").strip()
        aspect = resolve_aspect_ratio(aspect_ratio)
        if not prompt:
            return prompt_required_error(self.name, aspect)
        model = (str(load_image_gen_config(self.name).get("model") or "").strip()
                 or os.environ.get("KENARI_IMAGE_MODEL", "").strip() or DEFAULT_MODEL)
        fail = error_factory(self.name, aspect, model=model, prompt=prompt)
        openai, err = import_openai(self.name, aspect)
        if err:
            return err
        client = openai.OpenAI(api_key=get_secret_str("OPENAI_API_KEY", ""), base_url=get_secret_str("OPENAI_BASE_URL", ""))
        try:
            response = client.images.generate(model=model, prompt=prompt, size=size_for(aspect), n=1,
                                              response_format="b64_json")
        except Exception as exc:
            return fail(f"Image generation failed: {exc}", "api_error")
        data = getattr(response, "data", None) or []
        if not data:
            return fail("No image came back", "empty_response")
        image_ref, err = materialize_image(getattr(data[0], "b64_json", None), None, prefix="kenari", label="Kenari",
                                           provider=self.name, model=model, prompt=prompt, aspect=aspect)
        if err:
            return err
        return success_response(image=image_ref, model=model, prompt=prompt, aspect_ratio=aspect, provider=self.name)


def register(ctx) -> None:
    ctx.register_image_gen_provider(KenariImageProvider())
