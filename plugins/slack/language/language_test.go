package language

import (
	"slices"
	"testing"
)

func TestDetectTellsPortugueseFromAThread(t *testing.T) {
	t.Parallel()
	if got := Detect([]string{"Oi, você consegue revisar o contrato até amanhã?", "Consigo sim, vou olhar ainda hoje à tarde"}); got != "pt" {
		t.Errorf("Detect = %q, want pt", got)
	}
}

func TestDetectTellsEnglishFromAThread(t *testing.T) {
	t.Parallel()
	if got := Detect([]string{"Hey, could you review the contract by tomorrow?", "Sure, I will take a look this afternoon"}); got != "en" {
		t.Errorf("Detect = %q, want en", got)
	}
}

func TestDetectIsEmptyForATooShortThread(t *testing.T) {
	t.Parallel()
	for _, thread := range [][]string{
		{"ok", ":+1:", "<https://example.com|link>"},
		{},
		{"<@U0ANA1234> :eyes:"},
		{"```\nfunc main() { fmt.Println(\"hello world, this is a long line of code\") }\n```"},
	} {
		if got := Detect(thread); got != "" {
			t.Errorf("Detect(%q) = %q, want empty", thread, got)
		}
	}
}

func TestDetectIgnoresMentionsLinksAndCode(t *testing.T) {
	t.Parallel()
	thread := []string{
		"<@U0ANA1234> você viu o relatório que eu mandei ontem? <https://example.com/report/weekly-summary|the weekly summary report>",
		"Vi sim, só falta ajustar a tabela :tada: <#C0GERAL12|general>\n```\nSELECT name, total FROM the_report WHERE the_week IS NOT NULL AND the_owner = 'someone'\n```\ne usar `the new field from the export`",
	}
	if got := Detect(thread); got != "pt" {
		t.Errorf("Detect = %q, want pt", got)
	}
}

func BenchmarkDetectAFullThread(b *testing.B) {
	thread := make([]string, 50)
	for i := range thread {
		thread[i] = "Oi <@U0ANA1234>, você consegue revisar o contrato até amanhã? Vi o link <https://example.com/x> :eyes:"
	}
	for b.Loop() {
		Detect(thread)
	}
}

func TestDetectAccuracyOnShortThreads(t *testing.T) {
	t.Parallel()
	cases := []struct {
		want   string
		thread []string
	}{
		{"pt", []string{"Bom dia! Alguém sabe quando sai a nova versão do aplicativo?"}},
		{"pt", []string{"Preciso da sua ajuda com a planilha de custos", "Claro, me manda o link que eu olho"}},
		{"pt", []string{"A reunião de amanhã foi cancelada, vamos remarcar para quinta"}},
		{"pt", []string{"Você pode aprovar o pedido de compra?", "Aprovado!", "Obrigada"}},
		{"pt", []string{"O servidor caiu de novo, estou investigando o que aconteceu"}},
		{"pt", []string{"Quem ficou responsável pelo relatório mensal deste mês?"}},
		{"pt", []string{"Ainda estou esperando o retorno do cliente sobre a proposta"}},
		{"pt", []string{"Consegue me mandar os números atualizados até sexta-feira?", "Mando sim"}},
		{"pt", []string{"Terminei a revisão do texto, ficou muito bom, só mudei duas frases"}},
		{"pt", []string{"Vamos almoçar juntos hoje?", "Bora, meio-dia lá embaixo"}},
		{"pt", []string{"Não consegui acessar o painel, aparece um erro de permissão"}},
		{"pt", []string{"Lembrando que o prazo para enviar as notas é amanhã às dez horas"}},
		{"en", []string{"Good morning! Does anyone know when the new app version ships?"}},
		{"en", []string{"I need your help with the cost spreadsheet", "Sure, send me the link and I will check"}},
		{"en", []string{"Tomorrow's meeting was cancelled, let's move it to Thursday"}},
		{"en", []string{"Could you approve the purchase order?", "Approved!", "Thanks"}},
		{"en", []string{"The server went down again, I am looking into what happened"}},
		{"en", []string{"Who is in charge of the monthly report this month?"}},
		{"en", []string{"Still waiting for the client to get back to us on the proposal"}},
		{"en", []string{"Can you send me the updated numbers by Friday?", "Will do"}},
		{"en", []string{"I finished reviewing the text, it reads well, I only changed two sentences"}},
		{"en", []string{"Lunch together today?", "Sure, noon downstairs"}},
		{"en", []string{"I could not open the dashboard, it shows a permission error"}},
		{"en", []string{"Reminder that the deadline to send the grades is tomorrow at ten"}},
		{"es", []string{"Buenos días, ¿alguien sabe cuándo sale la nueva versión de la aplicación?"}},
		{"es", []string{"Necesito tu ayuda con la hoja de cálculo de costos", "Claro, mándame el enlace y la reviso"}},
		{"es", []string{"La reunión de mañana se canceló, la pasamos para el jueves"}},
		{"es", []string{"El servidor se cayó otra vez, estoy investigando qué pasó"}},
		{"es", []string{"Todavía estoy esperando la respuesta del cliente sobre la propuesta"}},
		{"es", []string{"¿Puedes enviarme los números actualizados antes del viernes?", "Sí, te los envío"}},
	}
	mixed := []struct {
		thread []string
		either []string
	}{
		{[]string{"can you take a look at the PR?", "sim, já olho"}, []string{"en", "pt"}},
		{[]string{"o deploy quebrou de novo", "yeah, rolling back now"}, []string{"en", "pt"}},
		{[]string{"ok, merged", "valeu! vou testar no staging"}, []string{"en", "pt"}},
		{[]string{"thanks!", "de nada, qualquer coisa me chama"}, []string{"en", "pt"}},
		{[]string{"o job de backfill quebrou de novo no staging", "vou olhar o log"}, []string{"pt"}},
	}
	for _, c := range mixed {
		if got := Detect(c.thread); got != "" && !slices.Contains(c.either, got) {
			t.Errorf("Detect(%q) = %q, want one of %v or empty", c.thread, got, c.either)
		}
	}
	right := 0
	for _, c := range cases {
		switch got := Detect(c.thread); got {
		case c.want:
			right++
		case "":
		default:
			t.Errorf("Detect(%q) = %q, want %s or empty", c.thread, got, c.want)
		}
	}
	t.Logf("%d of %d right", right, len(cases))
	if right < 24 {
		t.Errorf("%d of %d right, want at least 24", right, len(cases))
	}
}
