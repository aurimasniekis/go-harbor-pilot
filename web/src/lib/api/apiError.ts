/** An error response from the Harbor Pilot API (the error envelope). */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string | undefined
  readonly details: unknown

  constructor(status: number, code: string, message: string, requestId?: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.requestId = requestId
    this.details = details
  }
}
