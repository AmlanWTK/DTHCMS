package synthetic

// Bangladeshi names, in both scripts.
//
// The generator needs names a clinic operator would not blink at, in the two scripts the
// record actually uses. The profile says roughly 70% Muslim, 27% Hindu, 3% other, and that
// ~95% of records keep narrative text in Bangla — so a name is stored in both forms and the
// interface chooses.
//
// THESE TRANSLITERATIONS NEED DR. NAHID'S EYE. They are written carefully but by someone
// who does not speak Bangla, and a name that is subtly wrong is exactly the kind of thing
// that makes synthetic data feel synthetic to the only reviewer who matters. Corrections
// here cost nothing; the list is data, not logic.

// Name is one person's name in both scripts.
type Name struct {
	English string `json:"english"`
	Bangla  string `json:"bangla"`
}

type namePool struct {
	maleGiven   []Name
	femaleGiven []Name
	family      []Name
}

var muslimNames = namePool{
	maleGiven: []Name{
		{"Abdul Karim", "আব্দুল করিম"}, {"Mohammad Rahim", "মোহাম্মদ রহিম"},
		{"Shahidul", "শহীদুল"}, {"Nazrul", "নজরুল"}, {"Anwar", "আনোয়ার"},
		{"Mizanur", "মিজানুর"}, {"Rafiqul", "রফিকুল"}, {"Kamrul", "কামরুল"},
		{"Habibur", "হাবিবুর"}, {"Aminul", "আমিনুল"}, {"Delwar", "দেলোয়ার"},
		{"Faruk", "ফারুক"}, {"Masud", "মাসুদ"}, {"Tarek", "তারেক"},
		{"Sohel", "সোহেল"}, {"Jashim", "জসিম"}, {"Shamsul", "শামসুল"},
		{"Golam Mostafa", "গোলাম মোস্তফা"}, {"Ashraful", "আশরাফুল"}, {"Bellal", "বেলাল"},
	},
	femaleGiven: []Name{
		{"Fatema", "ফাতেমা"}, {"Rahima", "রহিমা"}, {"Nasrin", "নাসরিন"},
		{"Shirin", "শিরিন"}, {"Rokeya", "রোকেয়া"}, {"Salma", "সালমা"},
		{"Ayesha", "আয়েশা"}, {"Sultana", "সুলতানা"}, {"Jesmin", "জেসমিন"},
		{"Farida", "ফরিদা"}, {"Rashida", "রশিদা"}, {"Hasina", "হাসিনা"},
		{"Sabina", "সাবিনা"}, {"Rehana", "রেহানা"}, {"Shahnaz", "শাহনাজ"},
		{"Nargis", "নার্গিস"}, {"Taslima", "তাসলিমা"}, {"Momtaz", "মমতাজ"},
		{"Marzia", "মারজিয়া"}, {"Ruma", "রুমা"},
	},
	family: []Name{
		{"Islam", "ইসলাম"}, {"Rahman", "রহমান"}, {"Ahmed", "আহমেদ"},
		{"Hossain", "হোসেন"}, {"Uddin", "উদ্দিন"}, {"Khan", "খান"},
		{"Chowdhury", "চৌধুরী"}, {"Ali", "আলী"}, {"Miah", "মিয়া"},
		{"Sarker", "সরকার"}, {"Mollah", "মোল্লা"}, {"Bhuiyan", "ভূঁইয়া"},
		{"Talukder", "তালুকদার"}, {"Sheikh", "শেখ"}, {"Akter", "আক্তার"},
	},
}

var hinduNames = namePool{
	maleGiven: []Name{
		{"Rabindra", "রবীন্দ্র"}, {"Sujit", "সুজিত"}, {"Bimal", "বিমল"},
		{"Ashok", "অশোক"}, {"Dipak", "দীপক"}, {"Pradip", "প্রদীপ"},
		{"Nirmal", "নির্মল"}, {"Ranjit", "রঞ্জিত"}, {"Subrata", "সুব্রত"},
		{"Tapan", "তপন"}, {"Gopal", "গোপাল"}, {"Swapan", "স্বপন"},
		{"Bikash", "বিকাশ"}, {"Manoj", "মনোজ"}, {"Amit", "অমিত"},
	},
	femaleGiven: []Name{
		{"Anjali", "অঞ্জলি"}, {"Rekha", "রেখা"}, {"Sumita", "সুমিতা"},
		{"Mitali", "মিতালি"}, {"Purnima", "পূর্ণিমা"}, {"Shikha", "শিখা"},
		{"Aparna", "অপর্ণা"}, {"Bandana", "বন্দনা"}, {"Kalpana", "কল্পনা"},
		{"Sandhya", "সন্ধ্যা"}, {"Basanti", "বাসন্তী"}, {"Shefali", "শেফালী"},
		{"Jharna", "ঝর্ণা"}, {"Rina", "রীনা"}, {"Malati", "মালতী"},
	},
	family: []Name{
		{"Das", "দাস"}, {"Roy", "রায়"}, {"Ghosh", "ঘোষ"},
		{"Saha", "সাহা"}, {"Sarkar", "সরকার"}, {"Chakraborty", "চক্রবর্তী"},
		{"Biswas", "বিশ্বাস"}, {"Mondal", "মণ্ডল"}, {"Dutta", "দত্ত"},
		{"Paul", "পাল"}, {"Debnath", "দেবনাথ"}, {"Bhowmik", "ভৌমিক"},
		{"Majumder", "মজুমদার"}, {"Nath", "নাথ"}, {"Karmakar", "কর্মকার"},
	},
}

// The 3% that is neither. Kept small and deliberately unspecific rather than inventing a
// community's naming conventions badly.
var otherNames = namePool{
	maleGiven: []Name{
		{"Joseph", "জোসেফ"}, {"Michael", "মাইকেল"}, {"Anthony", "অ্যান্থনি"},
		{"Simon", "সাইমন"}, {"Peter", "পিটার"},
	},
	femaleGiven: []Name{
		{"Mary", "মেরি"}, {"Grace", "গ্রেস"}, {"Ruth", "রুথ"},
		{"Helena", "হেলেনা"}, {"Teresa", "তেরেসা"},
	},
	family: []Name{
		{"Gomes", "গোমেজ"}, {"Rozario", "রোজারিও"}, {"Costa", "কস্তা"},
		{"Cruze", "ক্রুজ"}, {"Palma", "পালমা"},
	},
}
