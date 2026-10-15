/** The tugboat app icon. Its colors are fixed in every theme. */
export function Mark({ className = '' }: { className?: string }) {
  return <img className={`mark ${className}`} src="/tug.png" alt="" aria-hidden="true" draggable={false} />
}
