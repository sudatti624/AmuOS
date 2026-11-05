import { describe, expect, test } from "vite-plus/test";
import { segments } from "@/components/chart";

describe("segments", () => {
  test("missing steps and missing values break the line", () => {
    const times = [0, 15, 30, 75, 90, 105];
    const values = [1, 2, 3, 4, null, 6];
    expect(segments(times, values, 15)).toEqual([
      [
        { t: 0, v: 1 },
        { t: 15, v: 2 },
        { t: 30, v: 3 },
      ],
      [{ t: 75, v: 4 }],
      [{ t: 105, v: 6 }],
    ]);
  });
  test("zero is a value, not a gap", () => {
    expect(segments([0, 15], [0, 0], 15)).toEqual([
      [
        { t: 0, v: 0 },
        { t: 15, v: 0 },
      ],
    ]);
  });
});
