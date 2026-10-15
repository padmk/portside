import { useState } from 'react'
import { api } from '../../api/client'
import type { ServerView } from '../../api/types'
import { initials, projectColor } from './format'

interface Props {
  server: Pick<ServerView, 'name' | 'hasIcon'>
  size?: number
}

/** The project's own icon when it has one, else a tile with initials tinted from the theme's project colors. */
export function ProjectTile({ server, size = 32 }: Props) {
  const [failed, setFailed] = useState(false)
  const showImg = server.hasIcon && !failed
  return (
    <span
      className={`tile${showImg ? ' tile--img' : ''}`}
      style={{ width: size, height: size, ['--tile' as string]: `var(--project-${projectColor(server.name)})`, fontSize: Math.max(12, size * 0.36) }}
      aria-hidden="true"
    >
      {showImg ? (
        <img src={api.iconUrl(server.name)} alt="" width={size} height={size} onError={() => setFailed(true)} loading="lazy" />
      ) : (
        initials(server.name)
      )}
    </span>
  )
}
