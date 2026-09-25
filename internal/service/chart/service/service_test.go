package service

import (
	"bytes"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chartModel "github.com/mechta-market/pulse_bot/internal/service/chart/model"
)

// CHART_OUT_DIR — куда сохранить картинки тестов, чтобы посмотреть глазами.
func save(t *testing.T, name string, img []byte) {
	if dir := os.Getenv("CHART_OUT_DIR"); dir != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), img, 0o600))
	}
}

func decodePng(t *testing.T, img []byte) (int, int) {
	cfg, err := png.DecodeConfig(bytes.NewReader(img))
	require.NoError(t, err)
	return cfg.Width, cfg.Height
}

func TestRender_Line(t *testing.T) {
	start := time.Date(2026, 9, 25, 3, 0, 0, 0, time.UTC)
	series := func(name string, base float64) chartModel.Series {
		points := make([]chartModel.Point, 0, 60)
		for i := range 60 {
			points = append(points, chartModel.Point{Time: start.Add(time.Duration(i) * 3 * time.Minute), Value: base + base*0.3*math.Sin(float64(i)/6)})
		}
		return chartModel.Series{Name: name, Points: points}
	}

	img, err := New(Config{}).Render(&chartModel.Spec{
		Type: chartModel.TypeLine, Title: "Память caravan за 3 часа", Unit: "bytes",
		Series: []chartModel.Series{series("caravan-api-7d9f", 520<<20), series("caravan-worker-5c6d", 310<<20)},
	})
	require.NoError(t, err)
	w, h := decodePng(t, img)
	assert.Equal(t, 1296, w)
	assert.Equal(t, 720, h)
	save(t, "line.png", img)
}

func TestRender_Bar(t *testing.T) {
	img, err := New(Config{}).Render(&chartModel.Spec{
		Type: chartModel.TypeBar, Title: "Ошибки в логах за сутки", Unit: "count",
		Series: []chartModel.Series{{Name: "ошибки", Points: []chartModel.Point{
			{Label: "seller", Value: 832}, {Label: "credit-broker", Value: 716}, {Label: "stg", Value: 245},
			{Label: "mb-broker", Value: 172}, {Label: "airflow-dags", Value: 118}, {Label: "delivery", Value: 107},
		}}},
	})
	require.NoError(t, err)
	decodePng(t, img)
	save(t, "bar.png", img)

	// несколько рядов — группы столбцов
	img, err = New(Config{}).Render(&chartModel.Spec{
		Type: chartModel.TypeBar, Title: "RPS: сейчас и вчера", Unit: "rps",
		Series: []chartModel.Series{
			{Name: "сейчас", Points: []chartModel.Point{{Label: "caravan", Value: 0.2}, {Label: "orders-center", Value: 0.5}}},
			{Name: "вчера", Points: []chartModel.Point{{Label: "caravan", Value: 0.3}, {Label: "orders-center", Value: 0.4}}},
		},
	})
	require.NoError(t, err)
	save(t, "bar_groups.png", img)
}

func TestRender_Invalid(t *testing.T) {
	s := New(Config{})

	_, err := s.Render(&chartModel.Spec{Type: chartModel.TypeLine})
	require.ErrorIs(t, err, ErrInvalidSpec)

	_, err = s.Render(&chartModel.Spec{Type: "pie", Series: []chartModel.Series{{Points: []chartModel.Point{{Label: "a", Value: 1}}}}})
	require.ErrorIs(t, err, ErrInvalidSpec)

	_, err = s.Render(&chartModel.Spec{Type: chartModel.TypeLine, Series: []chartModel.Series{{Points: []chartModel.Point{{Label: "a", Value: 1}}}}})
	require.ErrorIs(t, err, ErrInvalidSpec, "у линии нужно время")

	_, err = s.Render(&chartModel.Spec{Type: chartModel.TypeBar, Series: []chartModel.Series{{Points: []chartModel.Point{{Value: 1}}}}})
	require.ErrorIs(t, err, ErrInvalidSpec, "у столбца нужна подпись")
}

func TestScale(t *testing.T) {
	for _, tc := range []struct {
		unit   string
		max    float64
		factor float64
		label  string
	}{
		{"bytes", 522985472, 1.0 / (1 << 20), "МБ"},
		{"bytes", 3 << 30, 1.0 / (1 << 30), "ГБ"},
		{"bytes", 500, 1, "байт"},
		{"ratio", 0.4, 100, "%"},
		{"seconds", 0.12, 1000, "мс"},
		{"seconds", 3, 1, "с"},
		{"cores", 0.007, 100, "% ядра"},
		{"cores", 1.5, 1, "ядра"},
		{"rps", 0.25, 60, "запросов в минуту"},
		{"rps", 12, 1, "запросов в секунду"},
		{"count", 5, 1, ""},
		{"заказов", 5, 1, "заказов"},
	} {
		factor, label := scale(tc.unit, tc.max)
		assert.InDelta(t, tc.factor, factor, 1e-12, tc.unit)
		assert.Equal(t, tc.label, label, tc.unit)
	}
}

func TestTimeTicker(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Almaty")
	require.NoError(t, err)
	from := time.Date(2026, 9, 25, 8, 13, 0, 0, loc)
	to := from.Add(3 * time.Hour)

	ticks := timeTicker{loc: loc, format: "15:04"}.Ticks(float64(from.Unix()), float64(to.Unix()))
	labels := make([]string, 0, len(ticks))
	for _, tick := range ticks {
		labels = append(labels, tick.Label)
	}
	assert.Equal(t, []string{"08:30", "09:00", "09:30", "10:00", "10:30", "11:00"}, labels)
}
