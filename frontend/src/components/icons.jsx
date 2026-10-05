// Tiny inline SVG icons (stroke = currentColor), so no icon dependency is needed.
const base = (size) => ({
  width: size, height: size, viewBox: "0 0 24 24", fill: "none",
  stroke: "currentColor", strokeWidth: 2, strokeLinecap: "round", strokeLinejoin: "round",
});

export const IconBolt = ({ size = 18 }) => (
  <svg {...base(size)}><path d="M13 2 4 14h7l-1 8 9-12h-7l1-8Z" /></svg>
);
export const IconLayers = ({ size = 18 }) => (
  <svg {...base(size)}><path d="m12 2 9 5-9 5-9-5 9-5Z" /><path d="m3 12 9 5 9-5" /><path d="m3 17 9 5 9-5" /></svg>
);
export const IconPlay = ({ size = 18 }) => (
  <svg {...base(size)}><polygon points="6 4 20 12 6 20 6 4" /></svg>
);
export const IconCheck = ({ size = 18 }) => (
  <svg {...base(size)}><path d="M20 6 9 17l-5-5" /></svg>
);
export const IconX = ({ size = 18 }) => (
  <svg {...base(size)}><path d="M18 6 6 18" /><path d="m6 6 12 12" /></svg>
);
export const IconExternal = ({ size = 15 }) => (
  <svg {...base(size)}><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path d="M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5" /></svg>
);
export const IconArrowLeft = ({ size = 16 }) => (
  <svg {...base(size)}><path d="m12 19-7-7 7-7" /><path d="M19 12H5" /></svg>
);
