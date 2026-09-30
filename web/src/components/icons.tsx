// Inline SVG icons (stroke style), so the UI needs no icon font or external assets.
import type { JSX, SVGAttributes } from 'preact';

type P = SVGAttributes<SVGSVGElement>;

const S = (props: P & { children: JSX.Element | JSX.Element[] }) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" {...props} />
);

export const IconPhone = (p: P) => (
  <S {...p}>
    <path d="M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1 1 .4 1.9.7 2.8a2 2 0 0 1-.5 2.1L8 9.9a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.8.7a2 2 0 0 1 1.7 2z" />
  </S>
);
export const IconMic = (p: P) => (
  <S {...p}>
    <rect x="9" y="2" width="6" height="12" rx="3" />
    <path d="M19 10v1a7 7 0 0 1-14 0v-1M12 18v4M8 22h8" />
  </S>
);
export const IconMicOff = (p: P) => (
  <S {...p}>
    <path d="M2 2l20 20M9 9v2a3 3 0 0 0 5.1 2.1M15 9.3V5a3 3 0 0 0-5.9-.6" />
    <path d="M17 16.9A7 7 0 0 1 5 11v-1M19 10v1c0 .8-.1 1.5-.4 2.2M12 18v4M8 22h8" />
  </S>
);
export const IconPause = (p: P) => (
  <S {...p}>
    <rect x="6" y="4" width="4" height="16" rx="1" />
    <rect x="14" y="4" width="4" height="16" rx="1" />
  </S>
);
export const IconGrid = (p: P) => (
  <S {...p}>
    <circle cx="5" cy="5" r="1.4" />
    <circle cx="12" cy="5" r="1.4" />
    <circle cx="19" cy="5" r="1.4" />
    <circle cx="5" cy="12" r="1.4" />
    <circle cx="12" cy="12" r="1.4" />
    <circle cx="19" cy="12" r="1.4" />
    <circle cx="5" cy="19" r="1.4" />
    <circle cx="12" cy="19" r="1.4" />
    <circle cx="19" cy="19" r="1.4" />
  </S>
);
export const IconTransfer = (p: P) => (
  <S {...p}>
    <path d="M17 3l4 4-4 4M21 7H9M7 21l-4-4 4-4M3 17h12" />
  </S>
);
export const IconBackspace = (p: P) => (
  <S {...p}>
    <path d="M21 4H8l-7 8 7 8h13a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2zM18 9l-6 6M12 9l6 6" />
  </S>
);
export const IconArrowUpRight = (p: P) => (
  <S {...p}>
    <path d="M7 17L17 7M8 7h9v9" />
  </S>
);
export const IconArrowDownLeft = (p: P) => (
  <S {...p}>
    <path d="M17 7L7 17M16 17H7V8" />
  </S>
);
export const IconX = (p: P) => (
  <S {...p}>
    <path d="M18 6L6 18M6 6l12 12" />
  </S>
);
export const IconSettings = (p: P) => (
  <S {...p}>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
  </S>
);
export const IconShield = (p: P) => (
  <S {...p}>
    <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
  </S>
);
export const IconLogout = (p: P) => (
  <S {...p}>
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />
  </S>
);
export const IconVolume = (p: P) => (
  <S {...p}>
    <path d="M11 5L6 9H2v6h4l5 4V5zM15.5 8.5a5 5 0 0 1 0 7M19 5a10 10 0 0 1 0 14" />
  </S>
);
export const IconPlus = (p: P) => (
  <S {...p}>
    <path d="M12 5v14M5 12h14" />
  </S>
);
export const IconUser = (p: P) => (
  <S {...p}>
    <circle cx="12" cy="8" r="4" />
    <path d="M4 21a8 8 0 0 1 16 0" />
  </S>
);
