export type Theme = "dark" | "light" | "system";

const key = "amuos.theme";

export function storedTheme(): Theme {
  try {
    const value = localStorage.getItem(key);
    if (value === "light" || value === "system" || value === "dark") return value;
  } catch {
    // Fall back to the default theme.
  }
  return "dark";
}

export function applyTheme(theme: Theme) {
  const dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
}

export function saveTheme(theme: Theme) {
  try {
    localStorage.setItem(key, theme);
  } catch {
    // The choice still applies to this page.
  }
  applyTheme(theme);
}
