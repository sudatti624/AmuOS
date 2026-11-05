import { describe, expect, test } from "vite-plus/test";
import { formatBytes, formatDuration, formatPercent, formatRate, formatStep } from "@/lib/format";

describe("format", () => {
  test("bytes use binary units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
    expect(formatBytes(64 * 1024 ** 3)).toBe("64 GiB");
    expect(formatRate(2048)).toBe("2.0 KiB/s");
  });
  test("durations, percentages and steps", () => {
    expect(formatDuration(59)).toBe("0分");
    expect(formatDuration(3 * 86400 + 4 * 3600)).toBe("3日 4時間");
    expect(formatPercent(5.25)).toBe("5.3%");
    expect(formatPercent(42.4)).toBe("42%");
    expect(formatStep(15)).toBe("15秒");
    expect(formatStep(300)).toBe("5分");
    expect(formatStep(7200)).toBe("2時間");
  });
});
