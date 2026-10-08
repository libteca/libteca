# Processor input boundaries

Media serving opens a regular descriptor under the physical file's source root.
Native source-less podcast files use the data directory's podcasts subtree.
Jellyfin podcast streams also refuse descendant symlinks, including links that
remain inside that subtree. Configured source-root aliases remain supported.

FFmpeg and FFprobe receive only the selected descriptor as their primary input.
The input protocol whitelist contains the descriptor protocol alone (`fd`, or
`file` for `/dev/fd/3`). Caller output requirements do not widen this whitelist:
local HLS/image outputs and stdout remain ordinary output arguments.

Input format admission is limited to the container families used by the
advertised audio/video extensions: MOV/MP4/M4A/M4B, Matroska/WebM, AVI, MP3,
FLAC, Ogg/Opus, WAV, raw AAC and MPEG-4 video. All MOV demuxer aliases are
included. MOV external data references are explicitly disabled with
`enable_drefs=0`, with absolute alias paths disabled by
`use_absolute_path=0`. HLS, concat, DASH, image sequences and other nested-resource
input formats are refused even when named with an advertised extension. HLS
remains supported as generated playback output. External-reference movies and
manifest contents are not self-contained media files.

These are input admission restrictions, not a process filesystem sandbox or
native parser isolation. The processor retains host privileges. Primary
`os.Root` admission alone never contains a demuxer's secondary file opens.
The startup WAV probe establishes descriptor and option availability; it does
not establish comprehensive format compatibility or secondary-read safety.

The installed FFmpeg build's demuxer listing and MOV help were inspected.
Adversarial descriptor-form HLS/concat and file/pipe output regressions are
prepared, but media execution is still pending. MOV external-reference corpus,
every advertised real container/codec, supported distribution versions,
hardware fallback, native targets and deployment acceptance remain open.
The bounded format restriction requires independent review before landing.

# Core credentials

Core GET/HEAD media and EventSource routes may use the HttpOnly media cookie;
query-token authentication is refused on those routes. The cookie grants no
mutation authority. Core non-media routes, including writes, continue to
accept an explicitly supplied token through Authorization or the existing
`token` query parameter. First-party mutation callers use Bearer headers.
This is explicit credential possession, not ambient-cookie write authority.
Compatibility faces keep their established credential transports. Token
expiry, independent-token logout semantics, ABS possession capabilities and
Subsonic credential storage retain their recorded policy qualifications.
