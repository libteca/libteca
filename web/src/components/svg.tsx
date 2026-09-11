type IconProps = { size?: number };

function svgProps(size: number) {
  return {
    width: size, height: size, viewBox: "0 0 24 24",
    fill: "none", stroke: "currentColor", strokeWidth: 1.8,
    strokeLinecap: "round" as const, strokeLinejoin: "round" as const,
    style: { display: "block", flexShrink: 0 },
  };
}

export function IconSearch(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <circle cx="8.5" cy="8.5" r="5" />
      <path d="M12.5 12.5L19 19" />
    </svg>
  );
}

export function IconPlay(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)} strokeWidth={0}>
      <path d="M8 5.5v13l11-6.5z" fill="currentColor" />
    </svg>
  );
}

export function IconPause(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)} strokeWidth={0}>
      <rect x="7" y="5.5" width="3.4" height="13" rx="1" fill="currentColor" />
      <rect x="13.6" y="5.5" width="3.4" height="13" rx="1" fill="currentColor" />
    </svg>
  );
}

export function IconChevronLeft(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M14.5 5.5L8 12l6.5 6.5" />
    </svg>
  );
}

export function IconChevronRight(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M9.5 5.5L16 12l-6.5 6.5" />
    </svg>
  );
}

export function IconChevronDown(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 14)}>
      <path d="M6 9.5l6 6 6-6" />
    </svg>
  );
}

export function IconChevronUp(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 14)}>
      <path d="M6 14.5l6-6 6 6" />
    </svg>
  );
}

export function IconX(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 14)}>
      <path d="M6 6l12 12M18 6L6 18" />
    </svg>
  );
}

export function IconSpinner(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 20)} className="spin" aria-hidden>
      <circle cx="12" cy="12" r="8" strokeDasharray="36 20" />
    </svg>
  );
}

export function IconBack30(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)}>
      <path d="M11 5.5L5.5 10.5L11 15.5" />
      <path d="M5.5 10.5H14a5 5 0 0 1 0 10h-3" />
      <text x="12" y="9" fontSize="7.5" fill="currentColor" stroke="none" textAnchor="middle" fontFamily="inherit" fontWeight="600">30</text>
    </svg>
  );
}

export function IconFwd30(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 18)}>
      <path d="M13 5.5L18.5 10.5L13 15.5" />
      <path d="M18.5 10.5H10a5 5 0 0 0 0 10h3" />
      <text x="12" y="9" fontSize="7.5" fill="currentColor" stroke="none" textAnchor="middle" fontFamily="inherit" fontWeight="600">30</text>
    </svg>
  );
}

export function IconCC(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="3" y="5" width="18" height="14" rx="2.5" />
      <path d="M10.5 10.2a2.6 2.6 0 1 0 0 3.6M17 10.2a2.6 2.6 0 1 0 0 3.6" />
    </svg>
  );
}

export function IconVolume(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4 9.5v5h3.5L12 18.5v-13L7.5 9.5z" fill="currentColor" stroke="none" />
      <path d="M15 9a4.2 4.2 0 0 1 0 6M17.5 6.8a7.6 7.6 0 0 1 0 10.4" />
    </svg>
  );
}

export function IconVolumeOff(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4 9.5v5h3.5L12 18.5v-13L7.5 9.5z" fill="currentColor" stroke="none" />
      <path d="M15.5 9.5l5 5M20.5 9.5l-5 5" />
    </svg>
  );
}

export function IconFullscreen(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M8 4H4v4M16 4h4v4M8 20H4v-4M16 20h4v-4" />
    </svg>
  );
}

export function IconScan(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M20 12a8 8 0 1 1-2.3-5.6" />
      <path d="M20 3.5V7h-3.5" />
    </svg>
  );
}

export function IconLibrary(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4.5 5.5h4a2 2 0 0 1 2 2v11a1.8 1.8 0 0 0-2-1.8h-4zM19.5 5.5h-4a2 2 0 0 0-2 2v11a1.8 1.8 0 0 1 2-1.8h4z" />
    </svg>
  );
}

export function IconFilm(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="4" y="5" width="16" height="14" rx="2" />
      <path d="M4 9h16M4 15h16M8 5v14M16 5v14" />
    </svg>
  );
}

export function IconTv(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="3.5" y="6.5" width="17" height="11" rx="2" />
      <path d="M9 20.5h6" />
    </svg>
  );
}

export function IconMusic(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <circle cx="7" cy="17" r="2.6" />
      <path d="M9.5 17V5.5l9 2.2v3l-9-2.2" />
    </svg>
  );
}

export function IconHeadphones(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4.5 14v-1.5a7.5 7.5 0 0 1 15 0V14" />
      <rect x="3" y="13" width="4.2" height="6.5" rx="2" fill="currentColor" stroke="none" />
      <rect x="16.8" y="13" width="4.2" height="6.5" rx="2" fill="currentColor" stroke="none" />
    </svg>
  );
}

export function IconBook(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <path d="M4.5 6.5A2.5 2.5 0 0 1 7 4h9a2.5 2.5 0 0 1 2.5 2.5v6A2.5 2.5 0 0 1 16 15h-8.5l-3 3v-3.7A2.5 2.5 0 0 1 4.5 12z" />
    </svg>
  );
}

export function IconComic(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="4.5" y="4.5" width="15" height="15" rx="2" />
      <path d="M12 4.5v15M4.5 12h15" />
    </svg>
  );
}

export function IconPodcast(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 16)}>
      <rect x="9" y="3.5" width="6" height="10" rx="3" />
      <path d="M5.5 11.5a6.5 6.5 0 0 0 13 0M12 18v3M8.5 21h7" />
    </svg>
  );
}

export function IconCheck(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 14)}>
      <path d="M5 12.5l4.5 4.5L19 7.5" />
    </svg>
  );
}

export function IconMoon(p: IconProps) {
  return (
    <svg {...svgProps(p.size ?? 15)}>
      <path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10z" />
    </svg>
  );
}

export function TypeIcon({ type, size }: { type: string; size?: number }) {
  switch (type) {
    case "movies": return <IconFilm size={size} />;
    case "tv": return <IconTv size={size} />;
    case "music": return <IconMusic size={size} />;
    case "audiobooks": return <IconHeadphones size={size} />;
    case "books": return <IconBook size={size} />;
    case "comics": return <IconComic size={size} />;
    default: return <IconLibrary size={size} />;
  }
}
