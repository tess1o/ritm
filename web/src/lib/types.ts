// API types, generated from the OpenAPI description (docs/openapi.yaml → api-schema.d.ts by
// `make openapi`). This module gives them the names the app uses; do not hand-write API shapes.
import type * as S from './api-schema'

/** A collection response: `{ items }`, plus `from`/`to` for ranges (see the API conventions). */
export interface Items<T> {
  items: T[]
}
export interface RangeItems<T> extends Items<T> {
  from: string
  to: string
}

export type User = S.User
export type Account = S.Account
export type Session = S.Session
export type AuthConfig = S.AuthConfig
export type ApiKey = S.ApiKey
export type CreatedApiKey = S.CreatedApiKey
export type KeyScope = S.ApiKey['scope']

export type DailyActivity = S.DailyActivity
export type DailyHeart = S.DailyHeart
export type HeartEvent = S.HeartEvent
export type SleepNight = S.SleepNight
export type RecoveryReport = S.RecoveryReport
export type DayDetail = S.DayDetail

export type HRPoint = S.HrPoint
export type WorkoutSummary = S.WorkoutSummary
export type WorkoutList = S.WorkoutList
export type WorkoutDetail = S.WorkoutDetail
export type WorkoutType = S.WorkoutType
export type WorkoutRoute = S.Route

export type Stat = S.Stat
export type Overview = S.Overview
export type Freshness = S.FreshnessSummary
export type CalendarDay = S.CalendarDay
export type DataCalendar = S.DataCalendar
export type Insight = S.Insight
export type InsightList = S.InsightList
export type Polarity = Insight['polarity']

export type LoadDay = S.LoadDay
export type TrainingLoad = S.TrainingLoad
export type Weekly = S.Weekly

export type ImportStats = S.ImportStats
export type ImportRow = S.ImportRow
export type Import = S.Import
export type Progress = S.Progress

export type Settings = S.Settings
export type SettingsResponse = S.SettingsResponse
export type McpInfo = S.McpInfo
export type Coverage = S.Coverage
