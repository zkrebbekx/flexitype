import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

export function Spinner({ label }: { label: string }) {
  return (
    <p role="status" className="text-sm text-slate-500">
      {label}…
    </p>
  )
}

export function Alert({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="rounded border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-800">
      {children}
    </p>
  )
}

export function Button({ className = '', ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...props}
      className={
        'rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 ' +
        'disabled:cursor-not-allowed disabled:opacity-50 ' +
        className
      }
    />
  )
}

export function TextInput({ className = '', ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...props}
      className={
        'w-full rounded border border-slate-300 bg-white px-2 py-1.5 text-sm ' +
        'focus:border-slate-500 focus:outline-none ' +
        className
      }
    />
  )
}
