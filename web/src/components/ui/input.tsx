import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const field =
  "flex h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive";

export function Input({ className, ...props }: ComponentProps<"input">) {
  return <input className={cn(field, className)} {...props} />;
}

// Native select keeps keyboard and screen reader behavior predictable.
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return <select className={cn(field, "bg-background pr-8", className)} {...props} />;
}
