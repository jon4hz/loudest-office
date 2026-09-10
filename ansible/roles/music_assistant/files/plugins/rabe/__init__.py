"""RaBe (Radio Bern) music provider: one live radio station with songticker now-playing metadata."""

from __future__ import annotations

import time
import xml.etree.ElementTree as ET
from collections.abc import Sequence
from datetime import datetime
from pathlib import Path
from typing import TYPE_CHECKING

import aiohttp
from music_assistant.models.music_provider import MusicProvider
from music_assistant_models.enums import (
    ContentType,
    ImageType,
    MediaType,
    ProviderFeature,
    StreamType,
)
from music_assistant_models.errors import MediaNotFoundError
from music_assistant_models.media_items import (
    AudioFormat,
    BrowseFolder,
    MediaItemImage,
    MediaItemMetadata,
    MediaItemType,
    ProviderMapping,
    Radio,
    UniqueList,
)
from music_assistant_models.streamdetails import StreamDetails, StreamMetadata

if TYPE_CHECKING:
    from music_assistant import MusicAssistant
    from music_assistant.models import ProviderInstanceType
    from music_assistant_models.config_entries import ConfigEntry, ProviderConfig
    from music_assistant_models.provider import ProviderManifest

RADIO_ID = "rabe"
ICON = Path(__file__).with_name("icon.svg")
STREAM_URL = "https://stream.rabe.ch/livestream/rabe-hd.mp3"
TICKER_URL = "https://songticker.rabe.ch/songticker/0.9.3/current.xml"
NS = {"t": "http://rabe.ch/schema/ticker.xsd", "xlink": "http://www.w3.org/1999/xlink"}
METADATA_REFRESH_INTERVAL = 15
# music_assistant.controllers.streams.constants.STREAMDETAILS_INBAND_TITLE_HANDOFF_KEY
INBAND_TITLE_HANDOFF_KEY = "inband_title_handoff"
HTTP_TIMEOUT = aiohttp.ClientTimeout(total=10)


def _ts(el: ET.Element | None, tag: str) -> float | None:
    node = el.find(f"t:{tag}", NS) if el is not None else None
    return (
        datetime.fromisoformat(node.text).timestamp()
        if node is not None and node.text
        else None
    )


def parse_ticker(xml: str, now: float) -> StreamMetadata:
    """Map the songticker XML onto StreamMetadata: track as title/artist, show as album."""
    root = ET.fromstring(xml)
    show = root.find("t:show", NS)
    show_name = show.findtext("t:name", "RaBe", NS) if show is not None else "RaBe"
    link = show.find("t:link", NS) if show is not None else None
    uri = link.get(f"{{{NS['xlink']}}}href") if link is not None else None

    track = root.find("t:track", NS)
    if track is None:
        return StreamMetadata(title=show_name, uri=uri)
    md = StreamMetadata(
        title=track.findtext("t:title", "", NS),
        artist=track.findtext("t:artist", None, NS),
        album=show_name,
        uri=uri,
    )
    start, end = _ts(track, "startTime"), _ts(track, "endTime")
    if start is not None and end is not None and start <= now <= end:
        md.duration = int(end - start)
        md.elapsed_time = int(now - start)
        md.elapsed_time_last_updated = now
    return md


async def setup(
    mass: MusicAssistant, manifest: ProviderManifest, config: ProviderConfig
) -> ProviderInstanceType:
    """Initialize provider(instance) with given configuration."""
    return RabeProvider(mass, manifest, config, {ProviderFeature.BROWSE})


class RabeProvider(MusicProvider):
    """Provider implementation for RaBe."""

    @property
    def is_streaming_provider(self) -> bool:
        """Return True if the provider is a streaming provider."""
        return True

    async def get_config_entries(self) -> tuple[ConfigEntry, ...]:
        """Return Config entries to setup this provider."""
        return ()

    async def browse(self, path: str) -> Sequence[MediaItemType | BrowseFolder]:
        """Browse: a single radio station."""
        return [await self.get_radio(RADIO_ID)]

    async def get_radio(self, prov_radio_id: str) -> Radio:
        """Get full radio details by id."""
        if prov_radio_id != RADIO_ID:
            raise MediaNotFoundError(f"Unknown radio {prov_radio_id}")
        return Radio(
            provider=self.instance_id,
            item_id=RADIO_ID,
            name="RaBe",
            metadata=MediaItemMetadata(images=UniqueList([self._icon_image()])),
            provider_mappings={
                ProviderMapping(
                    provider_domain=self.domain,
                    provider_instance=self.instance_id,
                    item_id=RADIO_ID,
                    available=True,
                )
            },
        )

    def _icon_image(self) -> MediaItemImage:
        return MediaItemImage(
            type=ImageType.THUMB, path=ICON.name, provider=self.instance_id
        )

    async def resolve_image(self, path: str) -> str | bytes:
        """Serve the bundled logo for the radio item."""
        if path != ICON.name:
            raise MediaNotFoundError(f"Unknown image {path}")
        return ICON.read_bytes()

    async def get_stream_details(
        self, item_id: str, media_type: MediaType
    ) -> StreamDetails:
        """Get stream details for the live stream."""
        if item_id != RADIO_ID:
            raise MediaNotFoundError(f"Unknown radio {item_id}")
        details = StreamDetails(
            provider=self.instance_id,
            item_id=item_id,
            audio_format=AudioFormat(content_type=ContentType.MP3),
            media_type=MediaType.RADIO,
            stream_type=StreamType.HTTP,
            path=STREAM_URL,
            can_seek=False,
            allow_seek=False,
            stream_metadata_update_callback=self._update_metadata,
            stream_metadata_update_interval=METADATA_REFRESH_INTERVAL,
            data={INBAND_TITLE_HANDOFF_KEY: True},
        )
        await self._update_metadata(details, 0)
        return details

    async def _update_metadata(self, details: StreamDetails, _elapsed: int) -> None:
        """Refresh now-playing metadata from the songticker; keep the previous on failure."""
        try:
            async with self.mass.http_session.get(
                TICKER_URL, timeout=HTTP_TIMEOUT
            ) as resp:
                resp.raise_for_status()
                md = parse_ticker(await resp.text(), time.time())
        except (aiohttp.ClientError, TimeoutError, ET.ParseError, ValueError) as err:
            self.logger.debug("RaBe songticker unavailable: %s", err)
            return
        md.image_url = self.mass.metadata.get_image_url(self._icon_image())
        if md.artist:
            md.image_url, _, _ = await self.mass.metadata.get_image_url_by_name(
                md.artist,
                md.title,
                fallback_image_url=md.image_url,
                album_name=md.album,
            )
        details.stream_metadata = md
