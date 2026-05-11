import * as React from "react"
import { Check } from "lucide-react"
import { cn } from "@/lib/utils"

interface CheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "type"> {
  onCheckedChange?: (checked: boolean) => void;
}

const Checkbox = React.forwardRef<HTMLInputElement, CheckboxProps>(
  ({ className, onCheckedChange, onChange, checked, ...props }, ref) => (
    <label className="relative inline-flex items-center cursor-pointer">
      <input
        type="checkbox"
        ref={ref}
        checked={checked}
        className="sr-only peer"
        onChange={(e) => {
          onChange?.(e);
          onCheckedChange?.(e.target.checked);
        }}
        {...props}
      />
      <div
        className={cn(
          "h-4 w-4 shrink-0 rounded border border-primary bg-background",
          "peer-checked:bg-primary peer-checked:border-primary",
          "peer-disabled:cursor-not-allowed peer-disabled:opacity-50",
          "flex items-center justify-center",
          className
        )}
      >
        {checked && <Check className="h-3 w-3 text-primary-foreground" />}
      </div>
    </label>
  )
)
Checkbox.displayName = "Checkbox"

export { Checkbox }
