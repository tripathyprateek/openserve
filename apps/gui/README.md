# openserve GUI

The Next.js 14 frontend for openserve, an open-source LLM serving platform.

## Prerequisites

- Node.js 18+ and npm/yarn
- Environment variable: `NEXT_PUBLIC_API_URL` (defaults to empty string)

## Getting Started

### Installation

```bash
npm install
```

### Development

```bash
npm run dev
```

The app will be available at `http://localhost:3000`.

### Building

```bash
npm run build
npm start
```

### Type Checking

```bash
npm run typecheck
```

## Project Structure

- `app/` - Next.js App Router pages and layouts
  - `(main)/` - Protected routes with sidebar layout
    - `catalog/` - Model catalog page
    - `deployments/` - Deployment management
    - `keys/` - API key management
    - `usage/` - Usage and spend analytics
    - `audit/` - Audit log viewer
  - `layout.tsx` - Root layout with metadata
- `lib/` - Shared utilities and API client
  - `api.ts` - Type-safe API client with SWR integration
- `components/` - Reusable React components
  - `model-card.tsx` - Model display card
  - `deploy-dialog.tsx` - Deployment creation modal

## Features

- **Model Catalog**: Browse and deploy LLMs with configurable GPU classes, budgets, and token limits
- **Deployments**: Monitor running model instances with real-time status and endpoint management
- **API Keys**: Create and manage API keys with role-based access, rate limits, and IP allowlisting
- **Usage Analytics**: Access token usage and cost data via BigQuery integration
- **Audit Log**: Comprehensive audit trail for compliance and debugging

## Environment Setup

Create a `.env.local` file:

```env
NEXT_PUBLIC_API_URL=http://localhost:8000
```

## Tech Stack

- **Framework**: Next.js 14 with App Router
- **Language**: TypeScript (strict mode)
- **Styling**: Tailwind CSS 3
- **UI Components**: shadcn/ui (Radix UI)
- **Data Fetching**: SWR for client-side data management
- **Icons**: lucide-react
- **CSS Utilities**: clsx, tailwind-merge, tailwindcss-animate

## API Integration

The app uses a type-safe API client (`lib/api.ts`) that:

- Wraps all backend API calls with proper TypeScript types
- Implements error handling with detailed messages
- Uses SWR for efficient client-side caching and revalidation
- Supports both mutation and query operations

All API endpoints expect a `NEXT_PUBLIC_API_URL` base URL prefix.

## Styling

The app uses CSS variables for theming (light mode default):

- Primary color: Navy (`#0f172a`)
- Secondary colors: gray palette
- Customizable via `app/globals.css`

## Docker

The build output is configured as standalone for Docker:

```bash
docker build -t openserve-gui .
docker run -e NEXT_PUBLIC_API_URL=http://api:8000 -p 3000:3000 openserve-gui
```
