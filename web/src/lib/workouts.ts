// Icon names (components/Icon.vue) for HealthKit workout activity types.
const ICONS: [RegExp, string][] = [
  [/Walking|Hiking/, 'footprints'],
  [/Running/, 'run'],
  [/Swimming|Water|Surfing|Paddle|Rowing/, 'waves'],
  [/Cycling/, 'bike'],
  [/Strength|CrossTraining|Core/, 'dumbbell'],
]

/** HKWorkoutActivityTypeWalking → 'footprints'; anything unmapped → 'activity'. */
export function workoutIcon(activityType: string): string {
  return ICONS.find(([re]) => re.test(activityType))?.[1] ?? 'activity'
}
