import { useMemo } from "react";

import { svgPath } from "@/lib/code128";

// A Code 128 symbol, sized in modules and scaled by CSS so a sheet can be
// laid out in millimetres and still print at whatever the printer's
// resolution happens to be.
//
// quietZone is the blank margin either side. Ten modules is the standard's
// minimum; a scanner reads a barcode butted against a border as a wider
// bar and gives up.
export function Barcode({
  value,
  height = 12,
  quietZone = 10,
  className,
}: {
  value: string;
  height?: number;
  quietZone?: number;
  className?: string;
}) {
  const path = useMemo(() => {
    try {
      return svgPath(value);
    } catch {
      return null;
    }
  }, [value]);

  if (!path) {
    return <span className="font-mono text-xs text-danger-fg">unencodable: {value}</span>;
  }
  const total = path.width + quietZone * 2;
  return (
    <svg
      viewBox={`0 0 ${total} 1`}
      preserveAspectRatio="none"
      role="img"
      aria-label={`barcode ${value}`}
      style={{ height, width: "100%", display: "block" }}
      className={className}
    >
      {/* White beneath the bars, not transparent: a printed label on a
          coloured stock still needs its quiet zone to be blank. */}
      <rect x="0" y="0" width={total} height="1" fill="#fff" />
      <g transform={`translate(${quietZone} 0)`}>
        <path d={path.d} fill="#000" />
      </g>
    </svg>
  );
}
