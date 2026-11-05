import { useEffect, useId, useRef, useState, type PointerEvent } from "react";
import { formatTime } from "@/lib/format";

export interface ChartSeries {
  label: string;
  values: (number | null)[];
  color: string;
}

// segments splits a series at missing values and at missing steps, so a gap
// in the data is drawn as a gap and never bridged by a line.
export function segments(times: number[], values: (number | null)[], step: number) {
  const result: { t: number; v: number }[][] = [];
  let current: { t: number; v: number }[] = [];
  times.forEach((t, i) => {
    const v = values[i];
    const contiguous = i > 0 && t - times[i - 1] <= step;
    if (v === null || v === undefined || !contiguous) {
      if (current.length > 0) result.push(current);
      current = [];
    }
    if (v !== null && v !== undefined) current.push({ t, v });
  });
  if (current.length > 0) result.push(current);
  return result;
}

function niceMax(value: number) {
  if (value <= 0) return 1;
  const magnitude = 10 ** Math.floor(Math.log10(value));
  for (const factor of [1, 2, 2.5, 5, 10]) {
    if (value <= factor * magnitude) return factor * magnitude;
  }
  return 10 * magnitude;
}

const height = 170;
const left = 68;
const right = 8;
const top = 8;
const bottom = 24;

export function LineChart({
  title,
  times,
  step,
  from,
  to,
  series,
  format,
  max,
}: {
  title: string;
  times: number[];
  step: number;
  from: number;
  to: number;
  series: ChartSeries[];
  format: (value: number) => string;
  // A fixed scale, such as 100 for percentages.
  max?: number;
}) {
  const titleId = useId();
  const [hover, setHover] = useState<number | null>(null);
  // The drawing uses the real pixel width so text keeps its size.
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(480);
  useEffect(() => {
    const element = box.current;
    if (!element) return;
    const observer = new ResizeObserver(([entry]) =>
      setWidth(Math.max(240, Math.round(entry.contentRect.width))),
    );
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const observed = series.flatMap((s) => s.values.filter((v): v is number => v !== null));
  const yMax = max ?? niceMax(Math.max(0, ...observed));
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const x = (t: number) => left + ((t - from) / Math.max(1, to - from)) * plotWidth;
  const y = (v: number) => top + plotHeight - (Math.min(v, yMax) / yMax) * plotHeight;
  const long = to - from > 2 * 86400;
  const timeLabel = (t: number) =>
    long
      ? new Date(t * 1000).toLocaleDateString("ja-JP", { month: "numeric", day: "numeric" })
      : new Date(t * 1000).toLocaleTimeString("ja-JP", { hour: "2-digit", minute: "2-digit" });
  const last = times.length - 1;
  const shown = hover ?? (last >= 0 ? last : null);

  const move = (event: PointerEvent<SVGSVGElement>) => {
    if (times.length === 0) return;
    const rect = event.currentTarget.getBoundingClientRect();
    const t =
      from +
      ((((event.clientX - rect.left) / rect.width) * width - left) / plotWidth) * (to - from);
    let best = 0;
    times.forEach((time, i) => {
      if (Math.abs(time - t) < Math.abs(times[best] - t)) best = i;
    });
    setHover(best);
  };

  return (
    <figure className="flex flex-col gap-2 rounded-lg border bg-card p-4" aria-labelledby={titleId}>
      <figcaption className="flex flex-wrap items-baseline justify-between gap-2">
        <span id={titleId} className="text-sm font-medium">
          {title}
        </span>
        <span className="flex flex-wrap gap-3 text-xs text-muted-foreground">
          {shown !== null && <span>{formatTime(times[shown])}</span>}
          {series.map((s) => {
            const v = shown === null ? null : s.values[shown];
            return (
              <span key={s.label} className="flex items-center gap-1">
                <span
                  className="inline-block size-2 rounded-full"
                  style={{ background: s.color }}
                />
                {s.label} {v === null || v === undefined ? "データなし" : format(v)}
              </span>
            );
          })}
        </span>
      </figcaption>
      <div ref={box}>
        {times.length === 0 ? (
          <p className="py-10 text-center text-sm text-muted-foreground">
            この期間のデータはありません。
          </p>
        ) : (
          <svg
            viewBox={`0 0 ${width} ${height}`}
            className="h-auto w-full touch-none select-none"
            role="img"
            aria-label={`${title}のグラフ`}
            onPointerMove={move}
            onPointerLeave={() => setHover(null)}
          >
            {[0, 0.5, 1].map((f) => (
              <g key={f}>
                <line
                  x1={left}
                  x2={width - right}
                  y1={y(yMax * f)}
                  y2={y(yMax * f)}
                  stroke="var(--border)"
                  strokeDasharray={f === 0 ? undefined : "3 3"}
                />
                <text
                  x={left - 6}
                  y={y(yMax * f) + 4}
                  textAnchor="end"
                  fontSize="12"
                  fill="var(--muted-foreground)"
                >
                  {format(yMax * f)}
                </text>
              </g>
            ))}
            {[from, (from + to) / 2, to].map((t, i) => (
              <text
                key={t}
                x={x(t)}
                y={height - 6}
                textAnchor={i === 0 ? "start" : i === 2 ? "end" : "middle"}
                fontSize="12"
                fill="var(--muted-foreground)"
              >
                {timeLabel(t)}
              </text>
            ))}
            {series.map((s) =>
              segments(times, s.values, step).map((points, i) =>
                points.length === 1 ? (
                  <circle
                    key={`${s.label}-${i}`}
                    cx={x(points[0].t)}
                    cy={y(points[0].v)}
                    r="2"
                    fill={s.color}
                  />
                ) : (
                  <polyline
                    key={`${s.label}-${i}`}
                    points={points.map((p) => `${x(p.t)},${y(p.v)}`).join(" ")}
                    fill="none"
                    stroke={s.color}
                    strokeWidth="1.5"
                    strokeLinejoin="round"
                  />
                ),
              ),
            )}
            {hover !== null && (
              <line
                x1={x(times[hover])}
                x2={x(times[hover])}
                y1={top}
                y2={top + plotHeight}
                stroke="var(--muted-foreground)"
                strokeWidth="1"
              />
            )}
          </svg>
        )}
      </div>
    </figure>
  );
}
