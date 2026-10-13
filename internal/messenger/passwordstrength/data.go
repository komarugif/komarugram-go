// SPDX-License-Identifier: Unlicense OR MIT

package passwordstrength

// The dictionaries are short, and written for this package: the passwords,
// words and names people choose most, as far as such lists are known, the
// likeliest first. A password made of words that are not here is estimated
// by the way its letters read (wordlike) and not by the words.

// latinRow and cyrillicRow are the keys of the English and the Russian
// layouts, one above the other, for a word typed in the wrong one.
const (
	latinRow    = "qwertyuiop[]asdfghjkl;'zxcvbnm,."
	cyrillicRow = "йцукенгшщзхъфывапролджэячсмитьбю"
)

// keyboardRows are the rows of the keys, which are walked along.
var keyboardRows = []string{
	"qwertyuiop", "asdfghjkl", "zxcvbnm", "1234567890",
	"йцукенгшщзхъ", "фывапролджэ", "ячсмитьбю",
	"1qaz2wsx3edc4rfv", "qazwsxedcrfvtgb", "1q2w3e4r5t6y",
}

const commonPasswords = `
123456 password 12345678 qwerty 123456789 12345 1234 111111 1234567 dragon
123123 baseball abc123 football monkey letmein shadow master 666666 qwertyuiop
123321 mustang 1234567890 michael 654321 superman 1qaz2wsx 7777777 121212 000000
qazwsx 123qwe killer trustno1 jordan jennifer zxcvbnm asdfgh hunter buster
soccer harley batman andrew tigger sunshine iloveyou 2000 charlie robert thomas
hockey ranger daniel starwars 112233 george computer michelle jessica pepper
1111 zxcvbn 555555 11111111 131313 freedom 777777 pass maggie 159753 aaaaaa
ginger princess joshua cheese amanda summer love ashley nicole chelsea biteme
matthew access yankees 987654321 dallas austin thunder taylor matrix admin
welcome login passw0rd password1 qwerty123 1q2w3e4r 1q2w3e qwe123 admin123
root toor secret whatever hello hello123 monkey123 test test123 guest changeme
default letmein1 qwerty1 password123 abc12345 iloveyou1 qwertyui 1qazxsw2
zaq12wsx 1qaz2wsx3edc 1q2w3e4r5t 1q2w3e4r5t6y qazwsxedc q1w2e3r4 q1w2e3r4t5
asdasd asdfghjkl asdf1234 zxcvbnm123 qweasd qweasdzxc 11223344 12341234
1234qwer 123456a a123456 123456q q123456 qwe12345 1password password! p@ssw0rd
p@ssword passw0rd1 pa55word pa$$word welcome1 welcome123 letmein123 sunshine1
princess1 football1 baseball1 superman1 dragon123 master123 hunter2 starwars1
pokemon naruto minecraft fortnite roblox
пароль привет любовь йцукен ячсмить фыва пароль1 пароль123 привет123 любовь1
йцукен123 qwerty12 парольпароль 11111 1111111 111222 123654 321321 456789
789456 147258369 741852963 963852741 159357 951753 135790 246810
`

const commonWords = `
love life home house angel baby sweet honey happy lucky star sun moon sky blue
red green black white gold silver king queen prince princess dragon tiger lion
wolf eagle bear horse dog cat fish bird fire water earth wind ice snow rain
storm thunder lightning dark night day light shadow ghost devil god heaven hell
magic wizard ninja warrior hunter killer soldier army police power force speed
fast cool super mega ultra hello welcome secret private access login user admin
root guest master server system network internet computer laptop phone mobile
game play gamer music rock metal punk jazz dance party summer winter spring
autumn monday tuesday wednesday thursday friday saturday sunday january
february march april may june july august september october november december
money rich cash dollar bank work job school student teacher family mother
father sister brother friend buddy football soccer baseball basketball hockey
tennis golf racing car bike truck ford honda toyota mercedes bmw audi ferrari
porsche apple google microsoft facebook twitter telegram whatsapp youtube
android iphone windows linux ubuntu hacker secure security password passwords
mypassword letmein trust forever always never freedom liberty victory winner
champion legend hero captain pirate flower rose lily garden forest mountain
river ocean beach island city country world planet galaxy universe space rocket
orange banana cherry lemon coffee pizza cookie candy chocolate sugar butter
cheese pepper
любовь жизнь дом семья друг мама папа сестра брат солнце луна звезда небо море
река лес огонь вода земля ветер снег дождь ночь день свет тень ангел дракон
тигр волк медведь лев орёл кот собака лето зима весна осень москва россия
питер украина беларусь деньги работа школа привет пока спасибо пароль секрет
тайна админ компьютер телефон игра музыка футбол динамо спартак зенит цска
победа свобода счастье радость красота любимая любимый малыш солнышко зайка
котик принцесса король королева чемпион герой воин охотник тигр кошка
lyubov lubov privet parol moskva rossiya rossia ukraina sekret tajna spasibo
schaste mama papa solnce zvezda drakon volk kot koshka sobaka leto zima vesna
osen dengi rabota shkola futbol spartak zenit dinamo pobeda svoboda lyubimaya
lyubimyj malysh solnyshko zayka kotik printsessa korol koroleva
`

const commonNames = `
james john robert michael william david richard joseph thomas charles daniel
matthew anthony mark donald steven paul andrew joshua kevin brian george
edward ronald timothy jason jeffrey ryan jacob gary nicholas eric jonathan
stephen larry justin scott brandon frank benjamin gregory samuel raymond
patrick alexander jack dennis jerry tyler aaron henry peter adam nathan mary
patricia jennifer linda elizabeth barbara susan jessica sarah karen lisa nancy
betty margaret sandra ashley kimberly emily donna michelle carol amanda
melissa deborah stephanie rebecca laura sharon cynthia kathleen amy shirley
angela helen anna brenda pamela nicole emma samantha katherine christine debra
rachel catherine carolyn janet ruth maria heather diane virginia julie joyce
victoria olivia kelly christina lauren joan evelyn judith megan cheryl andrea
hannah martha jacqueline frances gloria ann teresa kathryn sara janice jean
alice madison doris abigail julia judy grace denise amber marilyn beverly
danielle theresa sophia marie diana brittany natalie isabella charlotte rose
alexis kayla max alex mike chris sam tom tim ben dan jim bob joe nick andy
александр сергей дмитрий андрей алексей максим иван николай михаил владимир
владислав артём артем денис евгений игорь олег павел роман антон кирилл
виктор виталий юрий анна мария елена ольга наталья наташа татьяна ирина
светлана екатерина катя юлия анастасия настя ксения марина виктория дарья
даша полина алина вероника саша маша миша ваня коля петя вася женя лена оля
таня света ира
aleksandr alexandr sergey sergei dmitry dmitriy dmitri andrey andrei alexey
aleksey aleksei maxim maksim ivan nikolay nikolai mikhail vladimir vladislav
artem artyom denis evgeny evgeniy eugene igor oleg pavel roman anton kirill
viktor vitaly vitaliy yuri yury yuriy anna mariya elena olga natalia natalya
natasha tatiana tatyana irina svetlana ekaterina katya yulia yuliya
anastasia nastya ksenia kseniya marina viktoria daria darya dasha polina alina
veronika sasha masha misha vanya kolya petya vasya zhenya lena olya tanya
sveta ira sanya seryozha dima andryusha lesha
`

// keyRows are the rows of the letter keys of the English and the Russian
// layouts, with how far each is moved to the right, as the rows of a keyboard
// are. The row of digits is not one: any string of digits is in it, and a
// number at random is not a mash.
var keyRows = []struct {
	keys   string
	offset float64
	row    int
}{
	{"qwertyuiop[]", .5, 1}, {"asdfghjkl;'", .75, 2}, {"zxcvbnm,./", 1.25, 3},
	{"йцукенгшщзхъ", .5, 1}, {"фывапролджэ", .75, 2}, {"ячсмитьбю", 1.25, 3},
}
