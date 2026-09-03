# Managing staff accounts

Instructions for the DTHC administrator. English first, বাংলা below. Everything here is
done from **Administration → Users** in the web console; nothing needs a developer.

Technical reference: [`../identity.md`](../identity.md) §11.

---

## English

### Before you start

You need the **System administrator** role, and your authenticator app on your phone.
Every change on these pages asks for a fresh six-digit code — one code per change. That is
deliberate: a console left open on a desk cannot invite someone or reset a password.

If you hold more than one role, choose **System administrator** in the _Acting as_ box at
the top of the page. Wearing another role the pages are read-only.

### Giving a new colleague an account (three minutes)

1. Open **Administration → Users** and press **Invite a colleague**.
2. Fill in the **employee code** (capitals, digits and underscores — for example `N006`),
   the name in **English** and in **Bengali**, and the phone number if you have it.
3. Under **First password**, press **Generate**, or type one of at least twelve
   characters. You will read this to the colleague; it is not sent anywhere.
4. Tick the **roles** the colleague will hold. As you tick, the box on the right shows
   exactly what those roles let the person do. Most staff hold one role; a doctor may hold
   two. Nobody needs **System administrator** to do clinical work.
5. Press **Create the account**, type the code from your authenticator, and confirm.
6. The employee code and the first password are shown **once**. Read them to the
   colleague, or write them on a slip you hand over and they destroy. When you press
   **Done** the password is gone from the screen for good.

The colleague signs in with the code and that password, and — if any of their roles
requires it — is walked through setting up an authenticator app (see
[`authenticator.md`](authenticator.md)). The password stays theirs until an administrator
sets another, so do not keep a copy.

### When somebody cannot sign in

Open their account from the list. The account page tells you why, and offers the fix:

| What you see                                        | What it means                                   | What to do                                                                                             |
| --------------------------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| Status **Suspended** or **Deactivated**             | Somebody paused or closed the account           | Press **Activate** if they should be back. The reason for the pause is shown under the status.         |
| _Authenticator needed_                              | Their role requires an app they have not set up | Nothing to do here — the app walks them through it at sign-in.                                         |
| They have forgotten the password                    |                                                 | **Set a password** → Generate → give a reason → confirm with your code. Read the new password to them. |
| They have lost the phone **and** the recovery codes | They cannot get past the second step            | **Reset authenticator** → reason → confirm. Their next sign-in sets up a new app.                      |
| They say a tablet is still signed in as them        | An open session on a device they no longer have | **Sign out everywhere**. They can sign in again immediately.                                           |

Every one of these ends the person's open sessions, so a password or authenticator you
have just changed cannot still be in use somewhere else.

You cannot reset your **own** authenticator — that is what your recovery codes are for —
and you cannot suspend yourself or remove your own administrator role. Ask the other
administrator.

### Changing what somebody may do

On the account page, under **Roles**:

- **Revoke** removes a role at once, on every device. You must give a reason; it is kept
  in the role history at the bottom of the page.
- **Grant another role** opens the role list. Tick the role, look at the permissions the
  box on the right says would be added (they are outlined), and press **Grant**.

Roles are never edited — a person either holds one or does not — so the history is always
a true record of who could do what, and when.

### Leaving, and coming back

- **Suspend** for somebody on leave or under review. A reason is required. Sign-in is
  refused until you **Activate** them again; nothing is deleted.
- **Deactivate** for somebody who has left. Their records and history stay, with their
  name on them, forever. If they return, **Activate** the same account — do not invite them
  again, which would split one person's history in two.

Accounts are never deleted, by design.

### If something goes wrong

- _"That did not complete"_ with a reference number: tell the developer the number.
- _"The clinic server cannot be reached"_: nothing was changed. Try again when the
  connection is back.
- A change refused with a message about the status: the account has moved since you
  opened the page. Reload and look again.

---

## বাংলা

### শুরু করার আগে

আপনার **সিস্টেম প্রশাসক** ভূমিকা থাকতে হবে, আর হাতে থাকতে হবে আপনার ফোনের অথেনটিকেটর
অ্যাপ। এই পাতাগুলোর প্রতিটি পরিবর্তনে নতুন একটি ছয় অঙ্কের কোড চাওয়া হয় — প্রতি পরিবর্তনে
একটি কোড। এটা ইচ্ছাকৃত: ডেস্কে খোলা রেখে যাওয়া কনসোল থেকে কেউ কাউকে আমন্ত্রণ জানাতে বা
পাসওয়ার্ড বদলাতে পারবে না।

একাধিক ভূমিকা থাকলে পাতার উপরের _যে ভূমিকায় কাজ করছেন_ ঘরে **সিস্টেম প্রশাসক** বেছে নিন।
অন্য ভূমিকায় থাকলে পাতাগুলো শুধু দেখা যায়, বদলানো যায় না।

### নতুন সহকর্মীকে অ্যাকাউন্ট দেওয়া (তিন মিনিট)

1. **প্রশাসন → ব্যবহারকারী** খুলে **সহকর্মীকে আমন্ত্রণ জানান** চাপুন।
2. **কর্মী কোড** (বড় হাতের ইংরেজি অক্ষর, সংখ্যা ও আন্ডারস্কোর — যেমন `N006`), **ইংরেজি** ও
   **বাংলা** নাম, আর জানা থাকলে ফোন নম্বর লিখুন।
3. **প্রথম পাসওয়ার্ড**-এর ঘরে **তৈরি করুন** চাপুন, অথবা কমপক্ষে বারো অক্ষরের একটি পাসওয়ার্ড
   লিখুন। এটি আপনি সহকর্মীকে পড়ে শোনাবেন; কোথাও পাঠানো হয় না।
4. সহকর্মী যে **ভূমিকা**গুলো পাবেন সেগুলোতে টিক দিন। টিক দেওয়ার সঙ্গে সঙ্গে ডান পাশের ঘরে
   দেখা যাবে ওই ভূমিকায় ঠিক কী কী করা যায়। বেশিরভাগ কর্মীর একটি ভূমিকা থাকে; একজন
   ডাক্তারের দুটি থাকতে পারে। ক্লিনিক্যাল কাজের জন্য কারও **সিস্টেম প্রশাসক** হওয়ার দরকার নেই।
5. **অ্যাকাউন্ট তৈরি করুন** চেপে অথেনটিকেটরের কোড লিখে নিশ্চিত করুন।
6. কর্মী কোড ও প্রথম পাসওয়ার্ড **একবারই** দেখানো হবে। সহকর্মীকে পড়ে শোনান, অথবা একটি
   কাগজে লিখে দিন যা তিনি পরে নষ্ট করে ফেলবেন। **হয়ে গেছে** চাপলে পাসওয়ার্ডটি পর্দা থেকে
   চিরতরে চলে যাবে।

সহকর্মী কোড ও ওই পাসওয়ার্ড দিয়ে সাইন ইন করবেন, আর তাঁর কোনো ভূমিকায় দরকার হলে অথেনটিকেটর
অ্যাপ সেট করতে ধাপে ধাপে নিয়ে যাওয়া হবে ([`authenticator.md`](authenticator.md) দেখুন)।
কোনো প্রশাসক নতুন পাসওয়ার্ড না দেওয়া পর্যন্ত এটিই তাঁর পাসওয়ার্ড, তাই কোনো কপি রাখবেন না।

### কেউ সাইন ইন করতে না পারলে

তালিকা থেকে তাঁর অ্যাকাউন্ট খুলুন। অ্যাকাউন্ট পাতাই বলে দেবে কারণ কী, আর সমাধান দেখাবে:

| যা দেখছেন                                           | এর মানে                                         | কী করবেন                                                                                                |
| --------------------------------------------------- | ----------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| অবস্থা **স্থগিত** বা **নিষ্ক্রিয়**                 | কেউ অ্যাকাউন্টটি থামিয়ে বা বন্ধ করে রেখেছেন    | ফিরে আসার কথা হলে **সক্রিয় করুন** চাপুন। থামানোর কারণ অবস্থার নিচে লেখা থাকে।                          |
| _অথেনটিকেটর প্রয়োজন_                               | তাঁর ভূমিকায় অ্যাপ লাগে, যা এখনো সেট করা হয়নি | এখানে কিছু করার নেই — সাইন ইনের সময় অ্যাপই তাঁকে ধাপে ধাপে নিয়ে যাবে।                                 |
| পাসওয়ার্ড ভুলে গেছেন                               |                                                 | **পাসওয়ার্ড দিন** → তৈরি করুন → কারণ লিখুন → কোড দিয়ে নিশ্চিত করুন। নতুন পাসওয়ার্ড তাঁকে পড়ে শোনান। |
| ফোন **এবং** রিকভারি কোড দুটোই হারিয়েছেন            | দ্বিতীয় ধাপ পার হতে পারছেন না                  | **অথেনটিকেটর রিসেট** → কারণ → নিশ্চিত করুন। পরের সাইন ইনে নতুন অ্যাপ সেট হবে।                           |
| বলছেন কোনো ট্যাবলেটে এখনো তাঁর নামে সাইন ইন করা আছে | এমন ডিভাইসে সেশন খোলা যা আর তাঁর কাছে নেই       | **সব জায়গা থেকে সাইন আউট**। তিনি সঙ্গে সঙ্গেই আবার সাইন ইন করতে পারবেন।                                |

এর প্রতিটিই ওই ব্যক্তির চালু সেশনগুলো শেষ করে দেয়, তাই এইমাত্র বদলানো পাসওয়ার্ড বা
অথেনটিকেটর অন্য কোথাও চালু থাকতে পারে না।

আপনি **নিজের** অথেনটিকেটর রিসেট করতে পারবেন না — সে জন্যই আপনার রিকভারি কোড — আর নিজেকে
স্থগিত করতে বা নিজের প্রশাসক ভূমিকা সরাতে পারবেন না। অন্য প্রশাসককে বলুন।

### কে কী করতে পারবেন তা বদলানো

অ্যাকাউন্ট পাতায়, **ভূমিকা**-র নিচে:

- **প্রত্যাহার** ভূমিকাটি সঙ্গে সঙ্গে সব ডিভাইসে সরিয়ে দেয়। কারণ লিখতেই হবে; পাতার নিচের
  ভূমিকার ইতিহাসে তা থেকে যায়।
- **আরেকটি ভূমিকা দিন** ভূমিকার তালিকা খোলে। ভূমিকায় টিক দিন, ডান পাশের ঘরে কোন অনুমতিগুলো
  নতুন যোগ হবে দেখুন (বর্ডার দিয়ে দেখানো), তারপর **দিন** চাপুন।

ভূমিকা কখনো সম্পাদনা করা হয় না — একজন হয় ভূমিকাটি পান, নয়তো পান না — তাই ইতিহাস সবসময়
সত্যি বলে: কে, কখন, কী করতে পারতেন।

### চলে যাওয়া, আর ফিরে আসা

- ছুটিতে বা পর্যালোচনায় থাকা কারও জন্য **স্থগিত করুন**। কারণ লাগবে। আবার **সক্রিয়** না করা
  পর্যন্ত সাইন ইন বন্ধ থাকে; কিছুই মোছা হয় না।
- যিনি চাকরি ছেড়েছেন তাঁর জন্য **নিষ্ক্রিয় করুন**। তাঁর রেকর্ড ও ইতিহাস তাঁর নামেই চিরকাল
  থাকে। ফিরে এলে একই অ্যাকাউন্ট **সক্রিয় করুন** — আবার আমন্ত্রণ জানাবেন না, তাতে এক ব্যক্তির
  ইতিহাস দুই ভাগ হয়ে যাবে।

অ্যাকাউন্ট কখনো মোছা হয় না — এটাই নকশা।

### কিছু ভুল হলে

- রেফারেন্স নম্বরসহ _"কাজটি সম্পন্ন হয়নি"_: নম্বরটি ডেভেলপারকে জানান।
- _"ক্লিনিকের সার্ভারে পৌঁছানো যাচ্ছে না"_: কিছুই বদলায়নি। সংযোগ ফিরলে আবার চেষ্টা করুন।
- অবস্থা নিয়ে বার্তা দিয়ে পরিবর্তন প্রত্যাখ্যাত হলে: আপনি পাতা খোলার পর অ্যাকাউন্টটি বদলে গেছে।
  পাতা রিলোড করে আবার দেখুন।
